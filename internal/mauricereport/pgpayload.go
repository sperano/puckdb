package mauricereport

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// CallInput returns the exact retained provider input of LLM call callID
// of ref's conversation: the call's metadata, the first InlineTextRunes of
// its system prompt, instruction and tool definitions (Payload reads the
// rest), and one page of its linked input messages, by input number, each
// with the start of its content whatever its role, except a withheld
// message (one of another conversation), which keeps only its ID. found is
// false when the conversation is not found or the call is not one of its
// calls.
func (s *PGStore) CallInput(ctx context.Context, ref ConversationRef, callID string, page PageRequest) (CallInput, bool, error) {
	r, err := validateRef(ref)
	if err != nil {
		return CallInput{}, false, err
	}
	id, err := parseRowID("callId", callID)
	if err != nil {
		return CallInput{}, false, err
	}
	var out CallInput
	list := detailList[InputMessage]{
		kind:   cursorCallInputs,
		parent: strconv.FormatInt(id, 10),
		read: func(ctx context.Context, tx pgx.Tx, after *cursor, limit int) ([]InputMessage, bool, error) {
			found, err := readCallHeader(ctx, tx, r, id, &out)
			if err != nil || !found {
				return nil, false, err
			}
			keys := afterKeys(after, 1)
			rows, err := tx.Query(ctx, `
				SELECT i.input_number, m.conversation_id <> $2::uuid, `+messageColumns+`
				FROM maurice_llm_call_messages i
				JOIN maurice_messages m ON m.id = i.message_id
				JOIN maurice_turns t ON t.id = m.turn_id
				CROSS JOIN LATERAL (SELECT CASE WHEN m.conversation_id = $2::uuid THEN m.content END AS content) v
				WHERE i.llm_call_id = $1 AND ($3::bigint IS NULL OR i.input_number > $3)
				ORDER BY i.input_number
				LIMIT $4`, id, r.conversationID.String(), keys[0], limit)
			inputs, err := collect(rows, err, "call inputs", scanInputMessage)
			return inputs, true, err
		},
		key: func(m InputMessage) cursor { return cursor{Keys: []int64{int64(m.Number)}} },
	}
	inputs, next, found, err := list.page(ctx, s, r, page)
	if err != nil || !found {
		return CallInput{}, false, err
	}
	out.Inputs, out.NextCursor = inputs, next
	return out, true, nil
}

// readCallHeader reads call id of r's conversation into out: its metadata
// and the start of its stored inputs. It reports false when the call is
// not of the conversation.
func readCallHeader(ctx context.Context, tx pgx.Tx, r detailRef, id int64, out *CallInput) (bool, error) {
	var system, instruction, tools textCols
	targets := callTargets(&out.Call)
	targets = append(targets, system.targets()...)
	targets = append(targets, instruction.targets()...)
	targets = append(targets, tools.targets()...)
	err := tx.QueryRow(ctx, `
		SELECT `+callColumnsDetail+`, `+inlineTextSQL("l.system_prompt")+`, `+
		inlineTextSQL("l.instruction")+`, `+inlineTextSQL("l.tool_definitions::text")+`
		FROM maurice_llm_calls l
		WHERE l.id = $1 AND l.conversation_id = $2::uuid`, id, r.conversationID.String()).Scan(targets...)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("call input: %w", err)
	}
	out.SystemPrompt, out.Instruction, out.ToolDefinitions = system.window(0), instruction.window(0), tools.window(0)
	return true, nil
}

// scanInputMessage reads one row of a call's inputs: the input number, the
// withheld flag, then messageColumns. A withheld message keeps only its ID.
func scanInputMessage(row pgx.CollectableRow) (InputMessage, error) {
	var (
		in      InputMessage
		status  string
		content textCols
	)
	m := &in.Message
	targets := append([]any{&in.Number, &in.Withheld, &m.ID, &m.TurnID, &m.TurnNumber, &status, &m.Number,
		&m.Role, &m.CreatedAt, &m.ContentLength}, content.targets()...)
	if err := row.Scan(append(targets, &m.ResultToolCallID, &m.ToolCalls)...); err != nil {
		return InputMessage{}, err
	}
	if in.Withheld {
		in.Message = Message{ID: m.ID}
		return in, nil
	}
	m.TurnStatus = TurnStatus(status)
	m.Content = content.window(0)
	return in, nil
}

// inlineTextSQL is the window columns of the first InlineTextRunes of value.
func inlineTextSQL(value string) string {
	return textSQL(value, "0", inlineSQL)
}

// ToolCall returns tool call toolCallID of ref's conversation with the
// first InlineTextRunes of its raw arguments, parsed arguments and result
// (Payload reads the rest). found is false when the conversation is not
// found or the tool call is not of one of its turns.
func (s *PGStore) ToolCall(ctx context.Context, ref ConversationRef, toolCallID string) (ToolCallDetail, bool, error) {
	r, err := validateRef(ref)
	if err != nil {
		return ToolCallDetail{}, false, err
	}
	id, err := parseRowID("toolCallId", toolCallID)
	if err != nil {
		return ToolCallDetail{}, false, err
	}
	var out ToolCallDetail
	found, err := s.inConversation(ctx, r, func(tx pgx.Tx) (bool, error) {
		var raw, parsed, result textCols
		targets := toolCallTargets(&out.ToolCall)
		targets = append(targets, raw.targets()...)
		targets = append(targets, parsed.targets()...)
		targets = append(targets, result.targets()...)
		err := tx.QueryRow(ctx, `
			SELECT `+toolCallColumns+`, `+inlineTextSQL("tc.arguments_raw")+`, `+
			inlineTextSQL("tc.arguments::text")+`, `+inlineTextSQL("tc.result")+`
			FROM maurice_tool_calls tc
			JOIN maurice_turns t ON t.id = tc.turn_id
			WHERE tc.id = $1 AND t.conversation_id = $2::uuid`, id, r.conversationID.String()).Scan(targets...)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("tool call: %w", err)
		}
		out.ArgumentsRaw = *raw.window(0)
		out.Arguments, out.Result = parsed.window(0), result.window(0)
		return true, nil
	})
	if err != nil || !found {
		return ToolCallDetail{}, false, err
	}
	return out, true, nil
}

// payloadSource is where a payload kind is stored: its row source x, the
// value and the match of row ID $1 within conversation $2.
type payloadSource struct {
	uuidID bool
	from   string
	value  string
	match  string
}

const (
	messageMatch  = "x.id = $1::uuid AND x.conversation_id = $2::uuid"
	callMatch     = "x.id = $1::bigint AND x.conversation_id = $2::uuid"
	toolCallFrom  = "maurice_tool_calls x JOIN maurice_turns t ON t.id = x.turn_id"
	toolCallMatch = "x.id = $1::bigint AND t.conversation_id = $2::uuid"
)

// payloadSources holds the source of every PayloadKind.
var payloadSources = map[PayloadKind]payloadSource{
	PayloadMessageContent:   {uuidID: true, from: "maurice_messages x", value: "x.content", match: messageMatch},
	PayloadSystemPrompt:     {from: "maurice_llm_calls x", value: "x.system_prompt", match: callMatch},
	PayloadInstruction:      {from: "maurice_llm_calls x", value: "x.instruction", match: callMatch},
	PayloadToolDefinitions:  {from: "maurice_llm_calls x", value: "x.tool_definitions::text", match: callMatch},
	PayloadToolArgumentsRaw: {from: toolCallFrom, value: "x.arguments_raw", match: toolCallMatch},
	PayloadToolArguments:    {from: toolCallFrom, value: "x.arguments::text", match: toolCallMatch},
	PayloadToolResult:       {from: toolCallFrom, value: "x.result", match: toolCallMatch},
}

// Payload returns a window of one retained payload of ref's conversation:
// up to req.Limit code points from req.Offset (past the end, an empty
// window). found is false when the conversation is not found, the message,
// call or tool call is not of it, or it stores no such value (no system
// prompt, arguments that did not parse, a tool call that never ran).
func (s *PGStore) Payload(ctx context.Context, ref ConversationRef, req PayloadRequest) (Text, bool, error) {
	r, err := validateRef(ref)
	if err != nil {
		return Text{}, false, err
	}
	src, ok := payloadSources[req.Kind]
	if !ok {
		return Text{}, false, fmt.Errorf("%w: unknown payload kind %q", ErrInvalidArgument, req.Kind)
	}
	var id any
	if src.uuidID {
		parsed, err := parseID("id", req.ID)
		if err != nil {
			return Text{}, false, err
		}
		id = parsed.String()
	} else if id, err = parseRowID("id", req.ID); err != nil {
		return Text{}, false, err
	}
	offset, limit, err := payloadWindow(req)
	if err != nil {
		return Text{}, false, err
	}
	var out *Text
	found, err := s.inConversation(ctx, r, func(tx pgx.Tx) (bool, error) {
		var cols textCols
		err := tx.QueryRow(ctx, `
			SELECT `+textSQL("p.v", "$3::int", "$4::int")+`
			FROM `+src.from+`
			CROSS JOIN LATERAL (SELECT `+src.value+` AS v) p
			WHERE `+src.match, id, r.conversationID.String(), offset, limit).Scan(cols.targets()...)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("payload %s: %w", req.Kind, err)
		}
		out = cols.window(offset)
		return out != nil, nil
	})
	if err != nil || !found {
		return Text{}, false, err
	}
	return *out, true, nil
}
