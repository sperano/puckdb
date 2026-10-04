// Package draftboard composes persisted Yahoo draft state, ranking snapshots,
// roster rules and deterministic recommendations for a live board consumer.
package draftboard

import (
	"time"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
)

// Request selects a league and the optional verified order and strategy.
type Request struct {
	League       string
	Season       int
	Scenario     draftrank.Scenario
	Order        []draftrecommend.PickSlot
	Strategy     draftrecommend.Strategy
	Availability draftrecommend.AvailabilitySource
	ADP          draftrecommend.ADPSource
}

// Pick is one effective history row with its Yahoo/manual provenance.
type Pick struct {
	Round     int
	Pick      int
	TeamID    int
	PlayerID  int
	PlayerKey string
	Name      string
	Source    draftsession.EntrySource
	Conflict  bool
	Undone    bool
	Cost      *int
}

// Player is one currently available ranked player and its scenario deltas.
type Player struct {
	PlayerKey      string
	YahooPlayerID  int
	Name           string
	Team           string
	Positions      []string
	Status         string
	InjuryNote     string
	BaselineRank   int
	SelectedRank   int
	BaselineValue  float64
	SelectedValue  float64
	News           []NewsSummary
	Recommendation *draftrecommend.Candidate
	IsShortlisted  bool
}

// NewsSummary is a dated adjustment reason from the immutable ranking.
type NewsSummary struct {
	Detail           string
	EffectiveFrom    time.Time
	LatestEvidenceAt time.Time
	Scenarios        []string
	Status           string
}

// Roster reports maximum-matching feasibility under Yahoo's league slots.
type Roster struct {
	Players     []draft.RosterPlayer
	Assignments []draft.SlotAssignment
	OpenSlots   map[string]int
	Warnings    []string
	Feasible    bool
}

// Status exposes persisted freshness and completeness without deriving turn
// order from draft format metadata.
type Status struct {
	Version             uint64
	SyncVersion         uint64
	DraftStatus         string
	RecommendationsSafe bool
	Complete            bool
	Stale               bool
	LastPollAt          *time.Time
	LastSuccessAt       *time.Time
	LastAuthoritativeAt *time.Time
	LastError           string
	Warnings            []string
}

// Event is one ordered state-changing event returned after a client cursor.
type Event struct {
	StateVersion uint64
	Kind         string
	Details      []byte
	CreatedAt    time.Time
}

// MutationResult returns the persisted reducer state after a local edit.
type MutationResult struct {
	Session draftwatch.Session
	Report  draftsession.Report
}

// Board is the composed API-facing live draft model.
type Board struct {
	Identity       draftwatch.Identity
	League         draftrank.League
	Snapshot       draftrank.SnapshotInfo
	Scenario       draftrank.Scenario
	DraftFormat    string
	PickClockSecs  *int
	TeamID         int
	TeamName       string
	DraftPosition  int
	Session        Status
	Picks          []Pick
	Roster         Roster
	Available      []Player
	Recommendation *draftrecommend.StoredRun
	Shortlist      []string
	CurrentPick    *draftrecommend.PickSlot
	PicksUntilTurn *int
	TurnEstimated  bool
	GeneratedAt    time.Time
}
