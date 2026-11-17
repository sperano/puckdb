// Package mauricereport is the read-only, cross-instance reporting contract
// over the Maurice tables: instance, user and conversation browsing and the
// usage overview (overview.go) for the private admin, with every user,
// conversation, aggregate and nested lookup scoped through
// app_users.instance. It returns metadata and aggregates only: never message
// content, prompts, tool arguments or tool results.
//
// Authorization is the caller's: the GraphQL @mauriceReporting directive
// (internal/graph) gates every field. The API contract (filters, date and
// search semantics, pagination, overview inclusion) is on the wiki page
// puckdb/maurice-reporting-api.md.
package mauricereport

import (
	"errors"
	"time"
)

// Page bounds shared by every list.
const (
	// DefaultLimit is the page size when the caller names none.
	DefaultLimit = 50
	// MaxLimit is the largest page size accepted; a larger one is
	// rejected, not clamped.
	MaxLimit = 200
)

// Input bounds.
const (
	// MaxSearchRunes bounds the conversation text search.
	MaxSearchRunes = 200
	// MaxNameRunes bounds the instance, provider, model and tool name
	// selectors.
	MaxNameRunes = 200
	// MaxCursorBytes bounds an encoded cursor before it is decoded.
	MaxCursorBytes = 512
)

var (
	// ErrInvalidArgument is a malformed or out-of-range input: a limit, a
	// date, a status, a name, a search string or an ID.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrInvalidCursor is a cursor this API did not issue for that list,
	// instance and filter.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrUserNotInInstance is a user ID that names no user of the selected
	// instance, whether it exists elsewhere or not.
	ErrUserNotInInstance = errors.New("user not found in instance")
)

// TurnStatus is a stored maurice_turns.status.
type TurnStatus string

// The stored turn statuses.
const (
	TurnRunning   TurnStatus = "running"
	TurnSucceeded TurnStatus = "succeeded"
	TurnFailed    TurnStatus = "failed"
	TurnCancelled TurnStatus = "cancelled"
)

// PageRequest selects one page of a list. A zero Limit is DefaultLimit; an
// empty Cursor is the first page.
type PageRequest struct {
	Limit  int
	Cursor string
}

// Instance is one API instance with users.
type Instance struct {
	Name      string
	UserCount int64
}

// InstancePage is one page of instances, by name. NextCursor is empty on
// the last page.
type InstancePage struct {
	Instances  []Instance
	NextCursor string
}

// User is one app_users row: profile metadata, no session or conversation
// content.
type User struct {
	ID               string
	Instance         string
	AuthProvider     string
	Username         string
	DisplayName      *string
	Email            *string
	FirstSeenAt      time.Time
	ProfileUpdatedAt time.Time
	DisabledAt       *time.Time
}

// UserPage is one page of an instance's users, newest first_seen_at first.
type UserPage struct {
	Users      []User
	NextCursor string
}

// TurnCounts counts stored turns by status: a conversation's, or every
// included turn of an overview's activity.
type TurnCounts struct {
	Running   int64
	Succeeded int64
	Failed    int64
	Cancelled int64
}

// Total is the number of turns.
func (c TurnCounts) Total() int64 {
	return c.Running + c.Succeeded + c.Failed + c.Cancelled
}

// Model is a provider and model pair.
type Model struct {
	Provider string
	Model    string
}

// Conversation is a conversation's metadata. Status is derived from the
// stored turns: the status of its latest turn (highest turn number), nil
// when it has none. Models are the distinct provider/model pairs of its
// turns' LLM calls (title generation, which has no turn, excluded), by
// provider then model.
type Conversation struct {
	ID                string
	Instance          string
	UserID            string
	Username          string
	Title             *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         *time.Time
	Status            *TurnStatus
	LastTurnStartedAt *time.Time
	Turns             TurnCounts
	Models            []Model
}

// ConversationPage is one page of conversations, newest created_at first.
type ConversationPage struct {
	Conversations []Conversation
	NextCursor    string
}

// ConversationFilter selects conversations. Instance is required; every
// other field left zero does not filter, except IncludeDeleted, whose zero
// value excludes deleted conversations. The turn fields (From, To,
// Statuses, Provider, Model, ToolName) must all hold for one turn of the
// conversation; Search and the owner fields apply to the conversation.
type ConversationFilter struct {
	// Instance is the owner's app_users.instance.
	Instance string
	// UserID is one user of Instance; another instance's user is
	// ErrUserNotInInstance.
	UserID string
	// From and To are inclusive UTC days (YYYY-MM-DD): a turn matches when
	// it started at or after From 00:00 UTC and before the day after To.
	From string
	To   string
	// Statuses matches a turn whose status is any of them.
	Statuses []TurnStatus
	// Provider and Model match a turn with an LLM call of that provider
	// and/or model (exact, case-sensitive; both on the same call).
	Provider string
	Model    string
	// ToolName matches a turn with a tool call of that name (exact).
	ToolName string
	// Search is a case-insensitive literal substring of the title or of the
	// content of a retained user or assistant message, of any turn. Tool
	// results, system messages, tool arguments and provider inputs are never
	// searched.
	Search string
	// IncludeDeleted also returns conversations their owner deleted.
	IncludeDeleted bool
}
