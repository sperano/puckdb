package mauricereport

import (
	"time"
	"unicode/utf8"
)

// The conversation detail reads (pgdetail*.go) return one conversation's
// transcript, turns, LLM calls and tool calls, and the payloads retained
// with them, to the private admin. Unlike the lists and the overview, they
// return content, but only on request: the transcript carries user and
// assistant message text; provider inputs (system prompt, instruction, tool
// definitions, linked messages), tool arguments and tool results are read
// only through CallInput, ToolCall and Payload. Every read re-checks the
// conversation (ConversationRef) and that each nested ID belongs to it.
// Contract on the wiki page puckdb/maurice-reporting-api.md ("Conversation
// detail").

// Payload bounds, in Unicode code points.
const (
	// InlineTextRunes bounds each text a detail response carries inline: a
	// transcript or input message's content, a call's system prompt,
	// instruction and tool definitions, tool arguments or a tool result.
	// The rest is read through Payload.
	InlineTextRunes = 8192
	// DefaultPayloadRunes is the window Payload returns when the caller
	// names no limit.
	DefaultPayloadRunes = 65536
	// MaxPayloadRunes bounds one Payload window; a larger limit is
	// rejected, not clamped.
	MaxPayloadRunes = 262144
	// MaxPayloadOffset bounds a Payload offset: no PostgreSQL text value
	// holds more than 1 GiB, so no stored text is longer.
	MaxPayloadOffset = 1 << 30
)

// ConversationRef selects the conversation of a detail read. It is found
// only when its owner belongs to Instance and, unless IncludeDeleted, the
// owner has not deleted it; otherwise every detail read reports not found,
// whether the conversation exists elsewhere or not.
type ConversationRef struct {
	Instance       string
	ConversationID string
	IncludeDeleted bool
}

// Text is a window of a stored text: Text is the code points of the stored
// value from Offset on, Length the stored value's length in code points.
type Text struct {
	Text   string
	Offset int64
	Length int64
}

// End is the offset just past the window.
func (t Text) End() int64 {
	return t.Offset + int64(utf8.RuneCountInString(t.Text))
}

// Truncated reports whether the stored text continues after the window.
func (t Text) Truncated() bool {
	return t.End() < t.Length
}

// Message is one retained maurice_messages row. Messages of a turn that did
// not succeed are kept as evidence (the exact prompts sent) but are never
// replayed to the model: see Replayable.
type Message struct {
	ID         string
	TurnID     string
	TurnNumber int32
	TurnStatus TurnStatus
	// Number is the message's position in its turn, from 0.
	Number    int32
	Role      string
	CreatedAt time.Time
	// ContentLength is the content's length in code points.
	ContentLength int64
	// Content is the start of the content: in the transcript only for user
	// and assistant messages (nil for tool results and system messages), in
	// a call's inputs for every role.
	Content *Text
	// ResultToolCallID is the tool call a tool message answers.
	ResultToolCallID *int64
	// ToolCalls counts the tool calls an assistant message requested.
	ToolCalls int64
}

// Replayable reports whether the message is part of the chat history
// Maurice replays: only messages of succeeded turns are.
func (m Message) Replayable() bool {
	return m.TurnStatus == TurnSucceeded
}

// MessagePage is one page of a conversation's transcript, by turn number
// then message number.
type MessagePage struct {
	Messages   []Message
	NextCursor string
}

// Turn is one maurice_turns row with counts and usage of its messages, LLM
// calls and tool calls; never their content.
type Turn struct {
	ID          string
	Number      int32
	Status      TurnStatus
	ErrorClass  *string
	StartedAt   time.Time
	CompletedAt *time.Time
	Warnings    []string
	Messages    int64
	ToolCalls   int64
	// Usage covers the turn's LLM calls; its CallLatency is over the
	// succeeded ones.
	Usage Usage
	// Models are the distinct provider/model pairs of its calls, by
	// provider then model.
	Models []Model
}

// Replayable reports whether the turn's messages are part of the chat
// history Maurice replays.
func (t Turn) Replayable() bool {
	return t.Status == TurnSucceeded
}

// TurnPage is one page of a conversation's turns, by turn number.
type TurnPage struct {
	Turns      []Turn
	NextCursor string
}

// Call is one maurice_llm_calls row's metadata. Token counts are nil when
// the provider did not report them, which differs from 0. The lengths, in
// code points, are nil when the call stored no such input.
type Call struct {
	ID int64
	// TurnID is nil for a title generation call, which belongs to no turn.
	TurnID *string
	Kind   string
	// Round is nil for a title generation call.
	Round                    *int32
	Provider                 string
	Model                    string
	ProviderRequestID        *string
	Status                   string
	ErrorClass               *string
	FinishReason             *string
	StartedAt                time.Time
	CompletedAt              time.Time
	InputTokens              *int64
	OutputTokens             *int64
	CacheCreationInputTokens *int64
	CacheReadInputTokens     *int64
	// InputMessages counts the messages linked as the request's input.
	InputMessages         int64
	SystemPromptLength    *int64
	InstructionLength     *int64
	ToolDefinitionsLength *int64
	ToolCalls             int64
}

// CallPage is one page of LLM calls: a turn's by round (ties by ID), or a
// conversation's title generation calls by start (ties by ID).
type CallPage struct {
	Calls      []Call
	NextCursor string
}

// InputMessage is one message linked as an LLM call's input, at Number in
// the request (the system prompt excluded). Withheld is set when the linked
// message belongs to another conversation: Message then holds only its ID.
type InputMessage struct {
	Number   int32
	Withheld bool
	Message  Message
}

// CallInput is the exact retained provider input of one LLM call: its
// stored system prompt, instruction and tool definitions (each nil when not
// stored) and one page of its linked input messages, by input number.
type CallInput struct {
	Call            Call
	SystemPrompt    *Text
	Instruction     *Text
	ToolDefinitions *Text
	Inputs          []InputMessage
	NextCursor      string
}

// ToolCall is one maurice_tool_calls row's metadata. CallID is nil for a
// call kept from the transcript without call telemetry, Status nil for
// such a transcript-only call; ArgumentsValid reports whether the raw
// arguments parsed as JSON (the parsed projection exists).
type ToolCall struct {
	ID                 int64
	TurnID             string
	CallID             *int64
	MessageID          *string
	Sequence           int32
	ProviderToolCallID *string
	Type               string
	Name               string
	Status             *string
	StartedAt          *time.Time
	CompletedAt        *time.Time
	ArgumentsValid     bool
	ArgumentsRawLength int64
	ResultLength       *int64
}

// ToolCallPage is one page of a turn's tool calls, by ID (recording order).
type ToolCallPage struct {
	ToolCalls  []ToolCall
	NextCursor string
}

// ToolCallDetail is a tool call with the start of its payloads: the raw
// arguments as the model sent them, the parsed projection (nil when they
// did not parse) and the result (nil when the call never ran).
type ToolCallDetail struct {
	ToolCall     ToolCall
	ArgumentsRaw Text
	Arguments    *Text
	Result       *Text
}

// PayloadKind names a retained payload Payload reads.
type PayloadKind string

// The payloads. ID is a message UUID for PayloadMessageContent, an LLM call
// ID for the call payloads and a tool call ID for the tool payloads.
const (
	PayloadMessageContent   PayloadKind = "message_content"
	PayloadSystemPrompt     PayloadKind = "system_prompt"
	PayloadInstruction      PayloadKind = "instruction"
	PayloadToolDefinitions  PayloadKind = "tool_definitions"
	PayloadToolArgumentsRaw PayloadKind = "tool_arguments_raw"
	PayloadToolArguments    PayloadKind = "tool_arguments"
	PayloadToolResult       PayloadKind = "tool_result"
)

// PayloadRequest selects a window of one payload: Limit code points from
// Offset. A zero Limit is DefaultPayloadRunes.
type PayloadRequest struct {
	Kind   PayloadKind
	ID     string
	Offset int64
	Limit  int
}
