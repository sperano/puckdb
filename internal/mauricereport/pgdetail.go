package mauricereport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// inlineSQL is the SQL literal of InlineTextRunes, the length of every text
// a detail response carries inline.
var inlineSQL = strconv.Itoa(InlineTextRunes)

// inConversation runs read in one read-only snapshot once r's conversation
// is found (owner of r's instance, deletion per r). found is false when the
// conversation is not, or when read reports a nested ID of another
// conversation; read never runs for a conversation not found.
func (s *PGStore) inConversation(ctx context.Context, r detailRef, read func(tx pgx.Tx) (bool, error)) (bool, error) {
	found := false
	err := pgx.BeginTxFunc(ctx, s.pool, snapshot, func(tx pgx.Tx) error {
		var args sqlArgs
		where := r.conds(&args)
		var one int
		err := tx.QueryRow(ctx, `SELECT 1 FROM maurice_conversations c JOIN app_users u ON u.id = c.user_id
			WHERE `+strings.Join(where, " AND "), args...).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find conversation: %w", err)
		}
		found, err = read(tx)
		return err
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// detailList is one paged detail list: its cursor kind, the parent the
// list is under (empty for a conversation-wide list), how to read rows
// after a position and the position of a row.
type detailList[T any] struct {
	kind   cursorKind
	parent string
	// read returns up to limit rows after the position (nil: from the
	// start), and false when the parent is not of the conversation.
	read func(ctx context.Context, tx pgx.Tx, after *cursor, limit int) ([]T, bool, error)
	key  func(T) cursor
}

// page reads one page of l in r's conversation: the rows, the next cursor
// (empty on the last page) and whether the conversation and parent were
// found.
func (l detailList[T]) page(ctx context.Context, s *PGStore, r detailRef, page PageRequest) ([]T, string, bool, error) {
	scope, err := detailScope(r, l.parent)
	if err != nil {
		return nil, "", false, err
	}
	limit, after, err := pageOf(l.kind, scope, page)
	if err != nil {
		return nil, "", false, err
	}
	var (
		rows []T
		next string
	)
	found, err := s.inConversation(ctx, r, func(tx pgx.Tx) (bool, error) {
		got, ok, err := l.read(ctx, tx, after, limit+1)
		if err != nil || !ok {
			return false, err
		}
		got, more := trimPage(got, limit)
		rows = got
		if more {
			c := l.key(got[len(got)-1])
			c.Kind, c.Scope = l.kind, scope
			next, err = encodeCursor(c)
		}
		return true, err
	})
	if err != nil || !found {
		return nil, "", false, err
	}
	return rows, next, true, nil
}

// afterKeys returns the cursor's integer keys, nil for the first page.
func afterKeys(after *cursor, n int) []*int64 {
	keys := make([]*int64, n)
	if after != nil {
		for i := range keys {
			keys[i] = &after.Keys[i]
		}
	}
	return keys
}

// Transcript lists the messages of ref's conversation, by turn number then
// message number: every retained message, those of failed, cancelled and
// running turns included (Message.Replayable tells them apart). User and
// assistant messages carry their content's first InlineTextRunes; tool
// results and system messages carry none (Payload reads them). found is
// false when the conversation is not found.
func (s *PGStore) Transcript(ctx context.Context, ref ConversationRef, page PageRequest) (MessagePage, bool, error) {
	r, err := validateRef(ref)
	if err != nil {
		return MessagePage{}, false, err
	}
	list := detailList[Message]{
		kind: cursorMessages,
		read: func(ctx context.Context, tx pgx.Tx, after *cursor, limit int) ([]Message, bool, error) {
			keys := afterKeys(after, 2)
			rows, err := tx.Query(ctx, `
				SELECT `+messageColumns+`
				FROM maurice_messages m
				JOIN maurice_turns t ON t.id = m.turn_id
				CROSS JOIN LATERAL (SELECT CASE WHEN m.role IN ('user', 'assistant') THEN m.content END AS content) v
				WHERE m.conversation_id = $1::uuid
				  AND ($2::bigint IS NULL OR (t.turn_number, m.message_number) > ($2, $3::bigint))
				ORDER BY t.turn_number, m.message_number
				LIMIT $4`, r.conversationID.String(), keys[0], keys[1], limit)
			messages, err := collect(rows, err, "transcript", scanMessage)
			return messages, true, err
		},
		key: func(m Message) cursor { return cursor{Keys: []int64{int64(m.TurnNumber), int64(m.Number)}} },
	}
	messages, next, found, err := list.page(ctx, s, r, page)
	return MessagePage{Messages: messages, NextCursor: next}, found, err
}

// messageColumns are the columns scanMessage reads, of a message m of turn
// t, v.content being its inline content (NULL for none).
var messageColumns = `m.id::text, m.turn_id::text, t.turn_number, t.status, m.message_number, m.role::text,
	m.created_at, char_length(m.content), substr(v.content, 1, ` + inlineSQL + `), char_length(v.content),
	m.result_tool_call_id, (SELECT count(*) FROM maurice_tool_calls tc WHERE tc.message_id = m.id)`

// textCols are the scan targets of a text window: its text and its stored
// length, both NULL when there is no stored value.
type textCols struct {
	text   *string
	length *int64
}

func (c *textCols) targets() []any {
	return []any{&c.text, &c.length}
}

// window is the scanned window from offset, nil when no value is stored.
func (c textCols) window(offset int64) *Text {
	if c.length == nil {
		return nil
	}
	t := Text{Offset: offset, Length: *c.length}
	if c.text != nil {
		t.Text = *c.text
	}
	return &t
}

// textSQL is the window columns textCols reads: limit code points of value
// from offset (SQL expressions; substr counts from 1).
func textSQL(value, offset, limit string) string {
	return "substr(" + value + ", " + offset + " + 1, " + limit + "), char_length(" + value + ")"
}

// scanMessage reads one row of messageColumns.
func scanMessage(row pgx.CollectableRow) (Message, error) {
	var (
		m       Message
		status  string
		content textCols
	)
	targets := append([]any{&m.ID, &m.TurnID, &m.TurnNumber, &status, &m.Number, &m.Role,
		&m.CreatedAt, &m.ContentLength}, content.targets()...)
	if err := row.Scan(append(targets, &m.ResultToolCallID, &m.ToolCalls)...); err != nil {
		return Message{}, err
	}
	m.TurnStatus = TurnStatus(status)
	m.Content = content.window(0)
	return m, nil
}

// collect gathers the rows of a query that returned err, naming what in
// errors.
func collect[T any](rows pgx.Rows, err error, what string, scan pgx.RowToFunc[T]) ([]T, error) {
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	out, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return out, nil
}
