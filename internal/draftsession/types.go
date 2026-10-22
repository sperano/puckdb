// Package draftsession contains pure draft-board reconciliation and manual
// advice-session state transitions. It has no persistence or Yahoo client
// dependencies so callers can apply its results inside their own transaction.
package draftsession

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidSnapshot rejects a Yahoo observation with a negative result
	// count. Reconcile skips invalid picks rather than rejecting them.
	ErrInvalidSnapshot = errors.New("invalid draft snapshot")
	// ErrInvalidManual rejects a manual operation or conflict resolution
	// that does not fit the slot's current state.
	ErrInvalidManual = errors.New("invalid manual draft operation")
	// ErrDuplicatePlayer rejects a manual change or conflict resolution that
	// would place one player in two effective slots. It wraps ErrInvalidManual.
	ErrDuplicatePlayer = fmt.Errorf("%w: player is already drafted", ErrInvalidManual)
)

// PickKey identifies a draft slot. Pick is the overall pick number within a
// round, matching the Yahoo draftresults resource.
type PickKey struct {
	Round int
	Pick  int
}

// Pick is one resolved board entry. TeamID and PlayerID are application IDs;
// unresolved Yahoo keys must be represented as malformed observed picks.
type Pick struct {
	Key      PickKey
	TeamID   int
	PlayerID int
	Cost     *int
}

// ObservedPick is a raw upstream row after key resolution. Invalid IDs or
// coordinates are reported and omitted from the usable board.
type ObservedPick struct {
	Key      PickKey
	TeamID   int
	PlayerID int
	Cost     *int
	Issue    string
}

// Snapshot carries the evidence needed to decide whether omissions are
// meaningful. ExpectedCount must be present and equal RawCount before an
// observation can remove old picks. An empty authoritative snapshot therefore
// requires HasExpectedCount=true and ExpectedCount=RawCount=0.
type Snapshot struct {
	Authoritative    bool
	HasExpectedCount bool
	ExpectedCount    int
	RawCount         int
	Picks            []ObservedPick
}

// EntrySource distinguishes Yahoo observations from advice-session entries.
type EntrySource string

const (
	// SourceYahoo is a pick Yahoo reported.
	SourceYahoo EntrySource = "yahoo"
	// SourceManual is a local add or correction Yahoo has not confirmed.
	SourceManual EntrySource = "manual"
)

// BoardEntry is an effective pick and its visible provenance.
type BoardEntry struct {
	Pick   Pick
	Source EntrySource
}

// ManualKind identifies an advice-session mutation.
type ManualKind string

const (
	// ManualAdd fills an empty slot.
	ManualAdd ManualKind = "add"
	// ManualCorrect replaces the pick in an occupied slot.
	ManualCorrect ManualKind = "correct"
	// ManualUndo empties an occupied slot: a tombstone hides a Yahoo pick
	// until Yahoo agrees, and a local add with no Yahoo pick beneath it is
	// simply removed.
	ManualUndo ManualKind = "undo"
)

// ManualOperation adds, corrects, or undoes a slot in the advice-session
// board. Pick is required for add/correct and ignored for undo.
type ManualOperation struct {
	Kind ManualKind
	Key  PickKey
	Pick *Pick
}

// ManualChange is a pending local intent. Base records what Yahoo showed when
// the user made the change; Conflict is raised if Yahoo later changes that
// slot to a third state, or if the change's player also occupies another
// effective slot.
type ManualChange struct {
	Kind     ManualKind
	Pick     *Pick
	Base     *Pick
	Conflict bool
}

// State is reducer state. Upstream and Manual are separate so a manual undo is
// retained as a tombstone instead of being mistaken for an empty Yahoo slot.
//
// UpstreamPlayers is the set of every player Yahoo has placed in any slot of
// this session. It only grows: a slot Yahoo later corrects or resets keeps its
// former player here, so a stale imported roster row for that player is still
// recognized as draft-derived (see DraftDerivedPlayers). It is derived from
// Upstream history and never changes without an Upstream change, so it does
// not take part in state equality or versioning.
type State struct {
	Upstream         map[PickKey]Pick
	Manual           map[PickKey]ManualChange
	UpstreamPlayers  map[int]bool
	UpstreamComplete bool
	Version          uint64
}

// SkippedPick records an upstream row that cannot safely enter the board.
type SkippedPick struct {
	Index  int
	Key    PickKey
	Reason string
}

// Report describes one transition and whether the resulting board is safe for
// recommendations that depend on all picks being known.
type Report struct {
	Changed         bool
	Complete        bool
	SafeToRecommend bool
	Applied         int
	Removed         int
	Skipped         []SkippedPick
	Conflicts       []PickKey
	Duplicates      []DuplicatePlayer
}

// DuplicatePlayer is one player that occupies more than one effective slot.
// Keys are in slot order.
type DuplicatePlayer struct {
	PlayerID int
	Keys     []PickKey
}

// ConflictChoice is an explicit resolution for a contested manual change.
type ConflictChoice string

const (
	// KeepManual keeps the manual change over Yahoo's current pick.
	KeepManual ConflictChoice = "keep_manual"
	// AcceptUpstream discards the manual change in favor of Yahoo's pick.
	AcceptUpstream ConflictChoice = "accept_upstream"
)
