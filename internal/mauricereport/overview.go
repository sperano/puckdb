package mauricereport

import "time"

// The overview reports usage and execution outcomes only: a succeeded turn
// is one that produced an answer, not a judgement of the answer's quality.
// Which conversations, turns and calls a filter includes, the timestamp each
// metric reads and the rate denominators are on the wiki page
// puckdb/maurice-reporting-api.md ("Overview").

// Overview aggregates the activity a ConversationFilter selects.
type Overview struct {
	// Users counts the users of the scope (the instance, or the filter's
	// one user), active or not.
	Users int64
	// ActiveUsers counts the users with at least one included turn.
	ActiveUsers int64
	Activity    Activity
	// Models breaks the included LLM calls down by provider and model, by
	// provider then model; title generation calls included.
	Models []ModelUsage
}

// UserActivity is one user of the scope and the activity of theirs the
// filter includes; a user without an included turn is not Active.
type UserActivity struct {
	User     User
	Active   bool
	Activity Activity
}

// UserActivityPage is one page of the scope's users, newest first_seen_at
// first (ties by ID), each with their included activity.
type UserActivityPage struct {
	Users      []UserActivity
	NextCursor string
}

// Activity aggregates included conversations, turns and LLM calls.
type Activity struct {
	// Conversations counts the included conversations.
	Conversations int64
	// Turns counts the included turns by stored status.
	Turns TurnCounts
	// FirstTurnStartedAt and LastTurnStartedAt bound the included turns'
	// starts; nil without turns.
	FirstTurnStartedAt *time.Time
	LastTurnStartedAt  *time.Time
	// TurnLatency is the duration of the included succeeded turns.
	TurnLatency Latency
	// Usage covers every included LLM call: the included turns' calls and
	// the included title generation calls.
	Usage Usage
	// TitleGeneration is the part of Usage made of title generation calls,
	// which belong to no turn.
	TitleGeneration Usage
	// ProcessingSeconds is the time Maurice spent processing prompts: per
	// conversation, the length of the union of its included terminal turns'
	// and title generation calls' [start, completion] intervals, summed over
	// conversations. A running turn adds nothing. It is server processing
	// time, not time a user spent in the UI.
	ProcessingSeconds float64
}

// ModelUsage is the included LLM calls of one provider and model.
type ModelUsage struct {
	Model Model
	// Turns counts the included turns with a call of this model; a turn
	// that called two models counts under both.
	Turns int64
	Usage Usage
}

// Usage aggregates LLM calls. Each token category is kept apart: a cache
// read is not an input token.
type Usage struct {
	Calls       int64
	FailedCalls int64

	InputTokens              TokenCount
	OutputTokens             TokenCount
	CacheCreationInputTokens TokenCount
	CacheReadInputTokens     TokenCount
	// CallLatency is the duration of the succeeded calls.
	CallLatency Latency
}

// TokenCount is one token category over a set of calls. A provider that
// does not report a count leaves it NULL, which is unknown, not zero.
type TokenCount struct {
	// Reported sums the count over the calls that reported it.
	Reported int64
	// ReportedCalls and UnreportedCalls split the calls by whether they
	// reported the count.
	ReportedCalls   int64
	UnreportedCalls int64
}

// Total is the category's total: Reported when every call reported it
// (zero calls included, a measured zero), a lower bound when only some did
// (see Complete), and nil, unknown, when calls ran and none reported it.
func (c TokenCount) Total() *int64 {
	if c.ReportedCalls == 0 && c.UnreportedCalls > 0 {
		return nil
	}
	total := c.Reported
	return &total
}

// Complete reports whether every call reported the count, so Total is
// exact.
func (c TokenCount) Complete() bool {
	return c.UnreportedCalls == 0
}

// Latency summarizes durations in seconds. Every statistic is nil when
// Count is zero; P50 and P95 are interpolated percentiles.
type Latency struct {
	Count int64
	Mean  *float64
	P50   *float64
	P95   *float64
	Max   *float64
}

// Terminal is the number of finished turns: succeeded, failed or
// cancelled. It is the denominator of every turn outcome rate; running turns
// have no outcome yet and are left out.
func (c TurnCounts) Terminal() int64 {
	return c.Succeeded + c.Failed + c.Cancelled
}

// Rate is n over the terminal turns, nil when there are none.
func (c TurnCounts) Rate(n int64) *float64 {
	terminal := c.Terminal()
	if terminal == 0 {
		return nil
	}
	rate := float64(n) / float64(terminal)
	return &rate
}
