package draft

import (
	"fmt"
	"slices"
	"time"
)

// PoolPlayer is one player of a league's draftable pool with the league's
// current Yahoo eligibility and status.
type PoolPlayer struct {
	YahooPlayerID int
	// PlayerKey is Yahoo's game-scoped key ("<game_key>.p.<player_id>").
	PlayerKey         string
	Name              string
	Team              string
	EligiblePositions []string
	Status            string
	StatusFull        string
	InjuryNote        string
	OnDisabledList    bool
	// NHLPlayerID is the NHL player the Yahoo ID maps to (players.yahoo_id),
	// or 0 when unmatched (e.g. a rookie without NHL history).
	NHLPlayerID int64
	FetchedAt   time.Time
}

// Matched reports whether the player maps to an NHL player.
func (p PoolPlayer) Matched() bool {
	return p.NHLPlayerID != 0
}

// RosterPlayer returns the player as a roster-feasibility input.
func (p PoolPlayer) RosterPlayer() RosterPlayer {
	return RosterPlayer{YahooPlayerID: p.YahooPlayerID, Name: p.Name, EligiblePositions: p.EligiblePositions}
}

// PoolCoverage summarizes how complete a league's player pool is. Players
// with gaps stay in the pool; the coverage report names them.
type PoolCoverage struct {
	Players int
	// MultiPosition counts players eligible at more than one base position.
	MultiPosition int
	// Unresolved players have no base-position eligibility from Yahoo.
	Unresolved []PoolPlayer
	// Unmatched players have no NHL player mapping.
	Unmatched []PoolPlayer
	// FetchedAt is the oldest fetch time in the pool (zero when empty).
	FetchedAt time.Time
	Stale     bool
}

// Coverage computes pool coverage; the pool is stale when its oldest fetch
// is more than maxAge before now.
func Coverage(players []PoolPlayer, now time.Time, maxAge time.Duration) PoolCoverage {
	coverage := PoolCoverage{Players: len(players)}
	for _, p := range players {
		if !p.RosterPlayer().Resolved() {
			coverage.Unresolved = append(coverage.Unresolved, p)
		}
		if !p.Matched() {
			coverage.Unmatched = append(coverage.Unmatched, p)
		}
		if countBasePositions(p.EligiblePositions) > 1 {
			coverage.MultiPosition++
		}
		if coverage.FetchedAt.IsZero() || p.FetchedAt.Before(coverage.FetchedAt) {
			coverage.FetchedAt = p.FetchedAt
		}
	}
	coverage.Stale = len(players) > 0 && now.Sub(coverage.FetchedAt) > maxAge
	return coverage
}

func countBasePositions(eligible []string) int {
	n := 0
	for _, position := range basePositions {
		if slices.Contains(eligible, position) {
			n++
		}
	}
	return n
}

// Warnings describes every coverage gap.
func (c PoolCoverage) Warnings() []string {
	if c.Players == 0 {
		return []string{"player pool is empty: run a Yahoo sync for this league"}
	}
	var warnings []string
	if c.Stale {
		warnings = append(warnings, "player pool is stale: fetched "+c.FetchedAt.UTC().Format(timeLayout))
	}
	if n := len(c.Unresolved); n > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of %d players have no Yahoo position eligibility", n, c.Players))
	}
	if n := len(c.Unmatched); n > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of %d players have no NHL player mapping (no NHL stats until matched)", n, c.Players))
	}
	return warnings
}
