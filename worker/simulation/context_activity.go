package simulation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// BuildManageRosterContextActivity — assembles the per-day, per-agent
// ManageRosterInput from DB state.
//
// The workflow goroutine can't read Postgres directly (deterministic-
// replay constraint), so the daily prompt context — standings, roster,
// agent notes — gets pre-fetched here and returned in one activity
// payload. ManageRosterActivity then receives it ready-to-use.
//
// V1 minimal context: the DailyPromptInput's expensive optional
// blocks (TodaysSchedule, TopFreeAgents, DailyAnalysis) are left
// empty. Rationale:
//
//   - TodaysSchedule needs ListSimDayGames + GetTeamGoalsPerGame
//     joins with team-name resolution. Useful for the LLM but not
//     load-bearing — the model can play without knowing which teams
//     have games today (it will just lean conservative on lineup).
//   - TopFreeAgents needs the recent-7-day ranking pipeline; the
//     workflow already passes the FreeAgents []int64 list, which is
//     enough for the validator's "is this a free agent?" check.
//   - DailyAnalysis is a derived summary the LLM could also compute
//     itself from the standings + roster blocks.
//
// All three are V2 polish. The agent's notes, current roster, and
// latest standings — the load-bearing pieces — ARE populated.
// ============================================================================

// BuildManageRosterContextInput is the workflow's request.
type BuildManageRosterContextInput struct {
	PoolID      int32       `json:"pool_id"`
	AgentID     int32       `json:"agent_id"`
	SimDate     pgtype.Date `json:"sim_date"`
	WorkflowID  string      `json:"workflow_id"`
	PoolConfig  PoolConfig  `json:"pool_config"`
	AgentConfig AgentConfig `json:"agent_config"`
	FreeAgents  []int64     `json:"free_agents"`
	OnWaivers   []int64     `json:"on_waivers"`
}

// BuildManageRosterContext returns a fully-populated
// ManageRosterInput the workflow can hand directly to
// ManageRosterActivity.
func (a *Activities) BuildManageRosterContext(ctx context.Context, in BuildManageRosterContextInput) (ManageRosterInput, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("BuildManageRosterContext",
		"pool_id", in.PoolID, "agent_id", in.AgentID, "sim_date", in.SimDate.Time,
	)

	notes, err := a.loadAgentNotes(ctx, in.AgentID)
	if err != nil {
		return ManageRosterInput{}, err
	}

	roster, positions, rosterRows, err := a.loadRosterAndPositions(ctx, in.PoolID, in.AgentID, in.PoolConfig)
	if err != nil {
		return ManageRosterInput{}, err
	}

	// Extend the position map to cover free-agent and waiver-pool players.
	// An agent may add one of these players in round 1 and then try to slot
	// them in round 2 of the same turn; without their position in the working
	// state the set_lineup validator would error with "player not in catalog".
	if err := a.loadCandidatePositions(ctx, in.FreeAgents, positions); err != nil {
		return ManageRosterInput{}, err
	}
	if err := a.loadCandidatePositions(ctx, in.OnWaivers, positions); err != nil {
		return ManageRosterInput{}, err
	}

	standingsRows, err := a.loadStandingsRows(ctx, in.PoolID, in.AgentID)
	if err != nil {
		return ManageRosterInput{}, err
	}

	// Players currently in their waiver-clearing window —
	// populated from ListSimPlayersOnWaivers so the agent's
	// claim_player tool calls validate against a real waiver pool.
	// Without this, ValidateClaimPlayer rejects every claim with
	// "player not on waivers" regardless of state.
	onWaivers, err := a.loadOnWaivers(ctx, in.PoolID, in.SimDate, in.PoolConfig.WaiverDays)
	if err != nil {
		return ManageRosterInput{}, fmt.Errorf("simulation: load on-waivers: %w", err)
	}

	// Players this agent already has an open claim on — feeds both the
	// working state (so a duplicate claim_player is rejected as a clean
	// action error rather than rolling back the whole turn on the unique
	// index) and the daily prompt (so the agent doesn't attempt the
	// duplicate in the first place).
	pendingClaims, pendingClaimRows, err := a.loadAgentPendingClaims(ctx, in.PoolID, in.AgentID)
	if err != nil {
		return ManageRosterInput{}, fmt.Errorf("simulation: load pending claims: %w", err)
	}

	prompt := DailyPromptInput{
		Day:            formatPgDateOrEmpty(in.SimDate),
		Standings:      standingsRows,
		YourRoster:     rosterRows,
		TodaysSchedule: nil, // V2 polish — see file header
		TopFreeAgents:  TopFreeAgents{},
		PendingClaims:  pendingClaimRows,
		YourNotes:      notes,
		YourAnalysis:   DailyAnalysis{},
	}

	return ManageRosterInput{
		PoolID:        in.PoolID,
		AgentID:       in.AgentID,
		SimDate:       in.SimDate,
		WorkflowID:    in.WorkflowID,
		PoolConfig:    in.PoolConfig,
		AgentConfig:   in.AgentConfig,
		DailyPrompt:   prompt,
		Roster:        roster,
		FreeAgents:    in.FreeAgents,
		OnWaivers:     onWaivers,
		Positions:     positions,
		PendingClaims: pendingClaims,
	}, nil
}

// loadAgentPendingClaims returns the player_ids the agent currently has
// a pending waiver claim on, plus the named rows rendered into the daily
// prompt. Empty (no open claims) is the common path.
func (a *Activities) loadAgentPendingClaims(ctx context.Context, poolID, agentID int32) ([]int64, []PendingClaimRow, error) {
	rows, err := a.Queries.ListSimWaiverClaimsPendingByAgent(ctx, sqlcdb.ListSimWaiverClaimsPendingByAgentParams{
		PoolID:  poolID,
		AgentID: agentID,
	})
	if err != nil {
		return nil, nil, err
	}
	ids := make([]int64, 0, len(rows))
	promptRows := make([]PendingClaimRow, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.PlayerID)

		p, err := a.Queries.GetPlayer(ctx, r.PlayerID)
		if err != nil {
			return nil, nil, fmt.Errorf("simulation: get claimed player %d: %w", r.PlayerID, err)
		}
		row := PendingClaimRow{
			Player:     p.FirstName + " " + p.LastName,
			ID:         r.PlayerID,
			ResolvesOn: formatPgDateOrEmpty(r.ProcessDate),
		}
		if r.DropPlayerID.Valid {
			dp, err := a.Queries.GetPlayer(ctx, r.DropPlayerID.Int64)
			if err != nil {
				return nil, nil, fmt.Errorf("simulation: get claim drop player %d: %w", r.DropPlayerID.Int64, err)
			}
			row.DropPlayer = dp.FirstName + " " + dp.LastName
		}
		promptRows = append(promptRows, row)
	}
	return ids, promptRows, nil
}

// loadOnWaivers queries the players whose waiver-clearing window
// covers `simDate`. The list goes into ManageRosterInput.OnWaivers
// so ValidateClaimPlayer recognizes a claimed player as actually
// being on waivers.
//
// Empty list (no recent drops) is the common path — early-season
// pools have nothing on waivers. Errors propagate; an empty result
// is not an error.
func (a *Activities) loadOnWaivers(ctx context.Context, poolID int32, simDate pgtype.Date, waiverDays int) ([]int64, error) {
	rows, err := a.Queries.ListSimPlayersOnWaivers(ctx, sqlcdb.ListSimPlayersOnWaiversParams{
		PoolID:  poolID,
		Column2: simDate,
		Column3: int32(waiverDays),
	})
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		if r.PlayerID.Valid {
			out = append(out, r.PlayerID.Int64)
		}
	}
	return out, nil
}

// loadAgentNotes returns sim_agents.notes for the agent.
func (a *Activities) loadAgentNotes(ctx context.Context, agentID int32) (string, error) {
	agent, err := a.Queries.GetSimAgent(ctx, agentID)
	if err != nil {
		return "", fmt.Errorf("simulation: get sim agent %d: %w", agentID, err)
	}
	return agent.Notes, nil
}

// loadRosterAndPositions returns three views of the same roster:
//
//   - RosterState (Placements + Limits) for the validators.
//   - Positions map (player_id → NHL position) for ValidateAndResolveLineup's catalog.
//   - []RosterRow for the daily prompt's your_roster block.
//
// One DB read for the rosters plus one GetPlayer per player (V1 N+1).
// Players with a NULL players.position are omitted from the position map
// with a warning; they will appear on the roster but cannot be slotted into
// any active slot (set_lineup will return a clear "position lookup" error).
func (a *Activities) loadRosterAndPositions(
	ctx context.Context,
	poolID, agentID int32,
	cfg PoolConfig,
) (RosterState, map[int64]sqlcdb.PlayerPosition, []RosterRow, error) {
	logger := activity.GetLogger(ctx)
	rows, err := a.Queries.ListSimRosterByAgent(ctx, sqlcdb.ListSimRosterByAgentParams{
		PoolID: poolID, AgentID: agentID,
	})
	if err != nil {
		return RosterState{}, nil, nil, fmt.Errorf("simulation: list roster (agent %d): %w", agentID, err)
	}

	placements := make(map[int64]RosterSlot, len(rows))
	positions := make(map[int64]sqlcdb.PlayerPosition, len(rows))
	rosterRows := make([]RosterRow, 0, len(rows))

	for _, r := range rows {
		placements[r.PlayerID] = RosterSlot(r.Slot)

		p, err := a.Queries.GetPlayer(ctx, r.PlayerID)
		if err != nil {
			return RosterState{}, nil, nil, fmt.Errorf("simulation: get player %d: %w", r.PlayerID, err)
		}
		if p.Position.Valid {
			positions[r.PlayerID] = p.Position.PlayerPosition
		} else {
			logger.Warn("roster player has NULL position — will not be slottable into active slots",
				"player_id", r.PlayerID,
				"name", p.FirstName+" "+p.LastName,
			)
		}
		rosterRows = append(rosterRows, RosterRow{
			Player:      p.FirstName + " " + p.LastName,
			ID:          r.PlayerID,
			NHLPosition: nullablePositionToString(p.Position),
			Slot:        RosterSlot(r.Slot),
			Team:        nullableInt8ToString(p.TeamID),
			// Last7 / Season / PlaysToday / GamesNext7Days are V2
			// polish — would need additional queries against game
			// stats + schedule. Empty values are valid (the LLM
			// sees zeros and treats them as "no recent data").
		})
	}

	limits := make(map[RosterSlot]int, len(cfg.RosterPositions))
	for k, v := range cfg.RosterPositions {
		limits[k] = v
	}

	return RosterState{Placements: placements, Limits: limits}, positions, rosterRows, nil
}

// loadCandidatePositions fetches NHL positions for a list of player IDs and
// merges them into the provided positions map. Players already in the map
// (i.e. currently on the roster) are skipped. Players with NULL position in
// the DB are silently omitted — they will produce a clear error from the
// set_lineup validator rather than a silent permanent failure.
//
// Called for FA and on-waivers candidates so that an agent who adds a player
// in round 1 can immediately slot them in round 2 of the same turn.
func (a *Activities) loadCandidatePositions(
	ctx context.Context,
	playerIDs []int64,
	positions map[int64]sqlcdb.PlayerPosition,
) error {
	for _, id := range playerIDs {
		if _, already := positions[id]; already {
			continue
		}
		p, err := a.Queries.GetPlayer(ctx, id)
		if err != nil {
			return fmt.Errorf("simulation: get player %d for position catalog: %w", id, err)
		}
		if p.Position.Valid {
			positions[id] = p.Position.PlayerPosition
		}
	}
	return nil
}

// loadStandingsRows reads the latest sim_standings snapshot and
// projects it into the StandingRow shape the daily prompt uses.
//
// is_you is set to true on the row whose agent_id matches in.AgentID
// — that's how the LLM identifies its own line in the table.
//
// Empty standings (day 1, before any games) → empty slice; valid
// state, no error. A transient DB error is propagated as an error so
// Temporal retries rather than presenting stale/empty standings.
func (a *Activities) loadStandingsRows(ctx context.Context, poolID, agentID int32) ([]StandingRow, error) {
	latest, err := a.Queries.GetSimStandingsLatestDate(ctx, poolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Pre-first-game: no standings rows exist yet.
			return nil, nil
		}
		return nil, fmt.Errorf("simulation: get standings latest date: %w", err)
	}
	if !latest.Valid {
		return nil, nil
	}
	rows, err := a.Queries.ListSimStandingsByDate(ctx, sqlcdb.ListSimStandingsByDateParams{
		PoolID: poolID, Date: latest,
	})
	if err != nil {
		return nil, fmt.Errorf("simulation: list standings for date: %w", err)
	}

	// Group standings rows by agent_id and category.
	// Each row carries both the category stat value and the roto_points
	// (written by UpdateStandings via UpsertSimStanding). We need both:
	// roto_points to derive rank (higher = better position), and value
	// for the Value field the LLM uses to see raw stat totals.
	type catEntry struct {
		value      float64 // raw category stat total
		rotoPoints float64 // fractional points earned in this category
	}
	byAgent := map[int32]map[string]catEntry{} // agent_id → category → entry
	totalRotoPts := map[int32]float64{}
	for _, r := range rows {
		pts, _ := numericToFloat(r.RotoPoints)
		val, _ := numericToFloat(r.Value)
		if _, ok := byAgent[r.AgentID]; !ok {
			byAgent[r.AgentID] = map[string]catEntry{}
		}
		byAgent[r.AgentID][r.Category] = catEntry{value: val, rotoPoints: pts}
		totalRotoPts[r.AgentID] += pts
	}

	// Derive true rank for each category from roto_points. Under standard
	// roto scoring, the team with the most roto_points in a category ranks
	// 1st. Tied agents receive the same rank (the 1-indexed position of the
	// first agent in their tie group). This is more robust than N - pts + 1
	// arithmetic because roto_points under ties are non-integer (e.g. 2.5 for
	// a 2-way tie at 3rd in a 5-team pool), and arithmetic rank derivation
	// would produce non-integer display ranks.
	categoryRank := func(cat string) map[int32]int {
		type entry struct {
			agentID    int32
			rotoPoints float64
		}
		entries := make([]entry, 0, len(byAgent))
		for aid, cats := range byAgent {
			entries = append(entries, entry{agentID: aid, rotoPoints: cats[cat].rotoPoints})
		}
		// Sort descending by roto_points; break ties by agent_id ascending
		// for determinism.
		for i := 1; i < len(entries); i++ {
			for j := i; j > 0; j-- {
				prev, cur := entries[j-1], entries[j]
				if prev.rotoPoints < cur.rotoPoints ||
					(prev.rotoPoints == cur.rotoPoints && prev.agentID > cur.agentID) {
					entries[j-1], entries[j] = entries[j], entries[j-1]
				}
			}
		}
		// Walk sorted runs: all agents in a run of equal roto_points get the
		// rank equal to the 1-indexed position of the first agent in that run.
		rank := make(map[int32]int, len(entries))
		p := 0
		for p < len(entries) {
			q := p
			for q < len(entries) && entries[q].rotoPoints == entries[p].rotoPoints {
				q++
			}
			firstRank := p + 1 // 1-indexed position of first agent in run
			for r := p; r < q; r++ {
				rank[entries[r].agentID] = firstRank
			}
			p = q
		}
		return rank
	}

	// Pre-load agent names so the table prints "Sonnet" not "agent 7".
	agents, err := a.Queries.ListSimAgentsByPool(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("simulation: list agents for standings: %w", err)
	}
	nameByID := make(map[int32]string, len(agents))
	for _, ag := range agents {
		nameByID[ag.ID] = displayName(ag)
	}

	catStanding := func(cat string, ranks map[string]map[int32]int, aid int32) CategoryStanding {
		e := byAgent[aid][cat]
		return CategoryStanding{
			Value: e.value,
			Rank:  float64(ranks[cat][aid]),
		}
	}

	// Build category rank maps once per category.
	ranksByCat := make(map[string]map[int32]int, len(byAgent))
	for cat := range map[string]struct{}{
		string(CategoryG): {}, string(CategoryA): {}, string(CategoryPM): {},
		string(CategoryPIM): {}, string(CategoryPPP): {}, string(CategorySOG): {},
		string(CategoryW): {}, string(CategoryGA): {}, string(CategoryGAA): {},
	} {
		ranksByCat[cat] = categoryRank(cat)
	}

	out := make([]StandingRow, 0, len(byAgent))
	for aid := range byAgent {
		out = append(out, StandingRow{
			Agent:        nameByID[aid],
			IsYou:        aid == agentID,
			G:            catStanding(string(CategoryG), ranksByCat, aid),
			A:            catStanding(string(CategoryA), ranksByCat, aid),
			PlusMinus:    catStanding(string(CategoryPM), ranksByCat, aid),
			PIM:          catStanding(string(CategoryPIM), ranksByCat, aid),
			PPP:          catStanding(string(CategoryPPP), ranksByCat, aid),
			SOG:          catStanding(string(CategorySOG), ranksByCat, aid),
			W:            catStanding(string(CategoryW), ranksByCat, aid),
			GA:           catStanding(string(CategoryGA), ranksByCat, aid),
			GAA:          catStanding(string(CategoryGAA), ranksByCat, aid),
			TotalRotoPts: totalRotoPts[aid],
		})
	}
	return out, nil
}

// nullablePositionToString unwraps NullPlayerPosition for prompt
// display. Empty when null.
func nullablePositionToString(p sqlcdb.NullPlayerPosition) string {
	if !p.Valid {
		return ""
	}
	return string(p.PlayerPosition)
}

// nullableInt8ToString turns a nullable Int8 (player.team_id) into
// a display string — V1 returns the raw ID; team_abbrev resolution
// is a V2 polish gate that needs season_teams join.
func nullableInt8ToString(t pgtype.Int8) string {
	if !t.Valid {
		return ""
	}
	return fmt.Sprintf("%d", t.Int64)
}

// formatPgDateOrEmpty formats a Date as YYYY-MM-DD, or returns the
// empty string for a NULL date.
func formatPgDateOrEmpty(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}
