package simulation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// BuildManageRosterContextActivity — assembles the per-day, per-agent
// ManageRosterInput from DB state.
//
// The workflow goroutine can't read Postgres directly (deterministic-
// replay constraint), so the daily prompt context — standings, roster,
// free agents, waiver pool, agent notes — gets pre-fetched here and
// returned in one activity payload. ManageRosterActivity then receives
// it ready-to-use.
//
// Populated blocks:
//   - standings, your_roster, your_notes, your_pending_claims — the
//     load-bearing pieces.
//   - top_free_agents — the ranked free-agent pool (top skaters/goalies
//     by recent composite, each carrying a valid player id for
//     add_player).
//   - on_waivers — the players currently in their waiver-clearing
//     window, each carrying a valid player id for claim_player.
//
// Still empty (documented V2 follow-ups):
//   - todays_schedule — needs ListSimDayGames + GetTeamGoalsPerGame
//     with team-abbrev resolution (see CHECKLIST.md "Known V2
//     Follow-ups").
//   - your_analysis — a derived summary the LLM can also compute itself
//     from the standings + roster blocks.
// ============================================================================

// BuildManageRosterContextInput is the workflow's request; its
// TurnScope (flattened on the wire) passes through to the
// ManageRosterInput it builds.
type BuildManageRosterContextInput struct {
	TurnScope

	FreeAgents []int64 `json:"free_agents"`
}

// BuildManageRosterContext returns a fully-populated
// ManageRosterInput the workflow can hand directly to
// ManageRosterActivity.
func (a *Activities) BuildManageRosterContext(ctx context.Context, in BuildManageRosterContextInput) (ManageRosterInput, error) {
	return rosterContextActivity{q: a.Queries}.BuildManageRosterContext(ctx, in)
}

// rosterContextQueries is BuildManageRosterContext's query group: the
// agent row (notes), its roster and the players behind it, the latest
// standings with the pool's agent names, the waiver pool, the free-
// agent ranking stats and the agent's own pending claims.
type rosterContextQueries interface {
	GetSimAgent(ctx context.Context, id int32) (sqlcdb.SimAgent, error)
	ListSimAgentsByPool(ctx context.Context, poolID int32) ([]sqlcdb.SimAgent, error)
	ListSimRosterByAgent(ctx context.Context, arg sqlcdb.ListSimRosterByAgentParams) ([]sqlcdb.SimRoster, error)
	GetSimStandingsLatestDate(ctx context.Context, poolID int32) (pgtype.Date, error)
	ListSimStandingsByDate(ctx context.Context, arg sqlcdb.ListSimStandingsByDateParams) ([]sqlcdb.SimStanding, error)
	ListSimPlayersOnWaivers(ctx context.Context, arg sqlcdb.ListSimPlayersOnWaiversParams) ([]sqlcdb.ListSimPlayersOnWaiversRow, error)
	ListSimFreeAgentSkaterStats(ctx context.Context, arg sqlcdb.ListSimFreeAgentSkaterStatsParams) ([]sqlcdb.ListSimFreeAgentSkaterStatsRow, error)
	ListSimFreeAgentGoalieStats(ctx context.Context, arg sqlcdb.ListSimFreeAgentGoalieStatsParams) ([]sqlcdb.ListSimFreeAgentGoalieStatsRow, error)
	// ListSimWaiverClaimsPendingByAgent feeds the daily working state so
	// a duplicate pending claim by the same agent is rejected as a clean
	// action error instead of violating the pending-one-per-agent-player
	// unique index.
	ListSimWaiverClaimsPendingByAgent(ctx context.Context, arg sqlcdb.ListSimWaiverClaimsPendingByAgentParams) ([]sqlcdb.SimWaiverClaim, error)
	playerLookupQueries
}

// rosterContextActivity runs BuildManageRosterContext over its own
// query group.
type rosterContextActivity struct {
	q rosterContextQueries
}

// BuildManageRosterContext is (*Activities).BuildManageRosterContext's
// body.
func (b rosterContextActivity) BuildManageRosterContext(ctx context.Context, in BuildManageRosterContextInput) (ManageRosterInput, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("BuildManageRosterContext",
		"pool_id", in.PoolID, "agent_id", in.AgentID, "sim_date", in.SimDate.Time,
	)

	notes, err := b.loadAgentNotes(ctx, in.AgentID)
	if err != nil {
		return ManageRosterInput{}, err
	}

	roster, positions, rosterRows, err := b.loadRosterAndPositions(ctx, in.PoolID, in.AgentID, in.PoolConfig)
	if err != nil {
		return ManageRosterInput{}, err
	}

	// Players currently in their waiver-clearing window — populated from
	// ListSimPlayersOnWaivers so the agent's claim_player tool calls
	// validate against a real waiver pool, and rendered into the daily
	// prompt so the agent has real ids to claim. Without this,
	// ValidateClaimPlayer rejects every claim with "player not on
	// waivers" regardless of state.
	onWaivers, waiverRows, err := b.loadOnWaivers(ctx, in.PoolID, in.SimDate, in.PoolConfig.WaiverDays)
	if err != nil {
		return ManageRosterInput{}, fmt.Errorf("simulation: load on-waivers: %w", err)
	}

	// Extend the position map to cover free-agent and waiver-pool players.
	// An agent may add a free agent in round 1 and then try to slot them in
	// round 2 of the same turn; without their position in the working state
	// the set_lineup validator would error with "player not in catalog".
	if err := b.loadCandidatePositions(ctx, in.FreeAgents, positions); err != nil {
		return ManageRosterInput{}, err
	}
	if err := b.loadCandidatePositions(ctx, onWaivers, positions); err != nil {
		return ManageRosterInput{}, err
	}

	standingsRows, err := b.loadStandingsRows(ctx, in.PoolID, in.AgentID)
	if err != nil {
		return ManageRosterInput{}, err
	}

	topFreeAgents, err := b.loadTopFreeAgents(ctx, in)
	if err != nil {
		return ManageRosterInput{}, fmt.Errorf("simulation: load top free agents: %w", err)
	}

	// Players this agent already has an open claim on — feeds both the
	// working state (so a duplicate claim_player is rejected as a clean
	// action error rather than rolling back the whole turn on the unique
	// index) and the daily prompt (so the agent doesn't attempt the
	// duplicate in the first place).
	pendingClaims, pendingClaimRows, err := b.loadAgentPendingClaims(ctx, in.PoolID, in.AgentID)
	if err != nil {
		return ManageRosterInput{}, fmt.Errorf("simulation: load pending claims: %w", err)
	}

	prompt := DailyPromptInput{
		Day:            pgconv.FormatDate(in.SimDate),
		Standings:      standingsRows,
		YourRoster:     rosterRows,
		TodaysSchedule: nil, // V2 polish — see file header
		TopFreeAgents:  topFreeAgents,
		OnWaivers:      waiverRows,
		PendingClaims:  pendingClaimRows,
		YourNotes:      notes,
		YourAnalysis:   DailyAnalysis{},
	}

	return ManageRosterInput{
		TurnScope:     in.TurnScope,
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
//
// Claimed + drop player names are resolved with one batched
// GetPlayersByIDs call (was up to 2 GetPlayer calls per row).
func (b rosterContextActivity) loadAgentPendingClaims(ctx context.Context, poolID, agentID int32) ([]int64, []PendingClaimRow, error) {
	rows, err := b.q.ListSimWaiverClaimsPendingByAgent(ctx, sqlcdb.ListSimWaiverClaimsPendingByAgentParams{
		PoolID:  poolID,
		AgentID: agentID,
	})
	if err != nil {
		return nil, nil, err
	}

	playerIDs := make([]int64, 0, len(rows)*2)
	for _, r := range rows {
		playerIDs = append(playerIDs, r.PlayerID)
		if r.DropPlayerID.Valid {
			playerIDs = append(playerIDs, r.DropPlayerID.Int64)
		}
	}
	players, err := loadPlayersByIDs(ctx, b.q, playerIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("simulation: batch load pending-claim players: %w", err)
	}

	ids := make([]int64, 0, len(rows))
	promptRows := make([]PendingClaimRow, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.PlayerID)

		p, ok := players[r.PlayerID]
		if !ok {
			return nil, nil, fmt.Errorf("simulation: get claimed player %d: not found", r.PlayerID)
		}
		row := PendingClaimRow{
			Player:     p.FirstName + " " + p.LastName,
			ID:         r.PlayerID,
			ResolvesOn: pgconv.FormatDate(r.ProcessDate),
		}
		if r.DropPlayerID.Valid {
			dp, ok := players[r.DropPlayerID.Int64]
			if !ok {
				return nil, nil, fmt.Errorf("simulation: get claim drop player %d: not found", r.DropPlayerID.Int64)
			}
			row.DropPlayer = dp.FirstName + " " + dp.LastName
		}
		promptRows = append(promptRows, row)
	}
	return ids, promptRows, nil
}

// loadOnWaivers queries the players whose waiver-clearing window
// covers `simDate`. It returns the player ids for the working state
// (ManageRosterInput.OnWaivers, so ValidateClaimPlayer recognizes a
// claimed player as actually being on waivers) and the named rows for
// the daily prompt's on_waivers block, so the agent can file a claim
// against a real id rather than inventing one.
//
// Empty list (no recent drops) is the common path — early-season
// pools have nothing on waivers. Errors propagate; an empty result
// is not an error.
func (b rosterContextActivity) loadOnWaivers(ctx context.Context, poolID int32, simDate pgtype.Date, waiverDays int) ([]int64, []WaiverPlayerRow, error) {
	rows, err := b.q.ListSimPlayersOnWaivers(ctx, sqlcdb.ListSimPlayersOnWaiversParams{
		PoolID:     poolID,
		SimDate:    simDate,
		WaiverDays: int32(waiverDays),
	})
	if err != nil {
		return nil, nil, err
	}

	ids := make([]int64, 0, len(rows))
	clearsOn := make(map[int64]pgtype.Date, len(rows))
	for _, r := range rows {
		if !r.PlayerID.Valid {
			continue
		}
		ids = append(ids, r.PlayerID.Int64)
		clearsOn[r.PlayerID.Int64] = r.ClearsOn
	}
	players, err := loadPlayersByIDs(ctx, b.q, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("simulation: batch load on-waivers players: %w", err)
	}

	promptRows := make([]WaiverPlayerRow, 0, len(ids))
	for _, id := range ids {
		p, ok := players[id]
		if !ok {
			return nil, nil, fmt.Errorf("simulation: get on-waivers player %d: not found", id)
		}
		promptRows = append(promptRows, WaiverPlayerRow{
			Player:      p.FirstName + " " + p.LastName,
			ID:          id,
			NHLPosition: pgconv.FormatPlayerPosition(p.Position),
			ClearsOn:    pgconv.FormatDate(clearsOn[id]),
		})
	}
	return ids, promptRows, nil
}

// loadTopFreeAgents builds the daily prompt's top_free_agents block:
// the free-agent pool split into skaters and goalies, each with their
// last-7-day and season-to-date stats, ranked through the pure
// RankFreeAgentSkaters / RankFreeAgentGoalies helpers.
//
// The returned candidates carry the player id the add_player tool
// validates against, so the agent is shown exactly the ids it may act
// on. F / NULL-position players are skipped — they cannot be slotted
// into any active slot, so presenting them as pickups would mislead.
func (b rosterContextActivity) loadTopFreeAgents(ctx context.Context, in BuildManageRosterContextInput) (TopFreeAgents, error) {
	if len(in.FreeAgents) == 0 {
		return TopFreeAgents{}, nil
	}
	players, err := loadPlayersByIDs(ctx, b.q, in.FreeAgents)
	if err != nil {
		return TopFreeAgents{}, fmt.Errorf("simulation: batch load free agent players: %w", err)
	}

	skaterIDs, goalieIDs := partitionFreeAgentIDs(in.FreeAgents, players)

	skaters, err := b.loadFreeAgentSkaters(ctx, in, skaterIDs, players)
	if err != nil {
		return TopFreeAgents{}, err
	}
	goalies, err := b.loadFreeAgentGoalies(ctx, in, goalieIDs, players)
	if err != nil {
		return TopFreeAgents{}, err
	}

	return TopFreeAgents{
		Skaters: RankFreeAgentSkaters(skaters, MaxTopFreeAgentSkaters),
		Goalies: RankFreeAgentGoalies(goalies, MaxTopFreeAgentGoalies),
	}, nil
}

// partitionFreeAgentIDs splits the free-agent pool into skaters and
// goalies by players.position. Players with no row, an F position, or
// a NULL position are dropped entirely — they cannot be slotted into
// an active slot, so showing them as pickups would mislead the agent.
func partitionFreeAgentIDs(ids []int64, players map[int64]sqlcdb.Player) (skaters, goalies []int64) {
	for _, id := range ids {
		p, ok := players[id]
		if !ok {
			continue // FA pool id with no players row — data gap
		}
		if isGoalie(p) {
			goalies = append(goalies, id)
			continue
		}
		if _, ok := skaterDraftPosition(p); ok {
			skaters = append(skaters, id)
		}
	}
	return skaters, goalies
}

// loadFreeAgentSkaters builds one FreeAgentSkaterCandidate per FA
// skater with a stats row. Iteration order of the stats rows doesn't
// matter: RankFreeAgentSkaters re-sorts by composite then id, so the
// final output is deterministic regardless of the query's return order.
func (b rosterContextActivity) loadFreeAgentSkaters(
	ctx context.Context,
	in BuildManageRosterContextInput,
	ids []int64,
	players map[int64]sqlcdb.Player,
) ([]FreeAgentSkaterCandidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := b.q.ListSimFreeAgentSkaterStats(ctx, sqlcdb.ListSimFreeAgentSkaterStatsParams{
		PlayerIds: ids,
		Season:    int32(in.PoolConfig.Season),
		GameDate:  in.SimDate,
	})
	if err != nil {
		return nil, fmt.Errorf("simulation: list free agent skater stats: %w", err)
	}
	out := make([]FreeAgentSkaterCandidate, 0, len(rows))
	for _, r := range rows {
		p, ok := players[r.PlayerID]
		if !ok {
			continue // stats row with no players row — data gap
		}
		out = append(out, FreeAgentSkaterCandidate{
			Player:      p.FirstName + " " + p.LastName,
			ID:          r.PlayerID,
			NHLPosition: pgconv.FormatPlayerPosition(p.Position),
			Last7: SkaterStats{
				G:         int(r.RecentGoals),
				A:         int(r.RecentAssists),
				PlusMinus: int(r.RecentPlusMinus),
				PIM:       int(r.RecentPim),
				PPP:       int(r.RecentPpp),
				SOG:       int(r.RecentSog),
			},
			Season: SkaterStats{
				G:         int(r.SeasonGoals),
				A:         int(r.SeasonAssists),
				PlusMinus: int(r.SeasonPlusMinus),
				PIM:       int(r.SeasonPim),
				PPP:       int(r.SeasonPpp),
				SOG:       int(r.SeasonSog),
			},
		})
	}
	return out, nil
}

// loadFreeAgentGoalies builds one FreeAgentGoalieCandidate per FA
// goalie with a stats row, deriving GAA from the stored GA/TOI
// components.
func (b rosterContextActivity) loadFreeAgentGoalies(
	ctx context.Context,
	in BuildManageRosterContextInput,
	ids []int64,
	players map[int64]sqlcdb.Player,
) ([]FreeAgentGoalieCandidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := b.q.ListSimFreeAgentGoalieStats(ctx, sqlcdb.ListSimFreeAgentGoalieStatsParams{
		PlayerIds: ids,
		Season:    int32(in.PoolConfig.Season),
		GameDate:  in.SimDate,
	})
	if err != nil {
		return nil, fmt.Errorf("simulation: list free agent goalie stats: %w", err)
	}
	out := make([]FreeAgentGoalieCandidate, 0, len(rows))
	for _, r := range rows {
		p, ok := players[r.PlayerID]
		if !ok {
			continue // stats row with no players row — data gap
		}
		out = append(out, FreeAgentGoalieCandidate{
			Player:      p.FirstName + " " + p.LastName,
			ID:          r.PlayerID,
			NHLPosition: pgconv.FormatPlayerPosition(p.Position),
			Last7: GoalieStats{
				W:   int(r.RecentWins),
				GA:  int(r.RecentGa),
				GAA: goalsAgainstAverage(int(r.RecentGa), int(r.RecentToi)),
			},
			Season: GoalieStats{
				W:   int(r.SeasonWins),
				GA:  int(r.SeasonGa),
				GAA: goalsAgainstAverage(int(r.SeasonGa), int(r.SeasonToi)),
			},
		})
	}
	return out, nil
}

// goalsAgainstAverage returns GAA = GA / TOI * 3600, or 0 when the
// goalie has no recorded ice time (no games in the window).
func goalsAgainstAverage(ga, toiSeconds int) float64 {
	if toiSeconds <= 0 {
		return 0
	}
	return float64(ga) / float64(toiSeconds) * 3600
}

// loadAgentNotes returns sim_agents.notes for the agent.
func (b rosterContextActivity) loadAgentNotes(ctx context.Context, agentID int32) (string, error) {
	agent, err := b.q.GetSimAgent(ctx, agentID)
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
// One DB read for the rosters plus one batched GetPlayersByIDs call
// covering every roster player (was one GetPlayer per player).
// Players with a NULL players.position are omitted from the position map
// with a warning; they will appear on the roster but cannot be slotted into
// any active slot (set_lineup will return a clear "position lookup" error).
func (b rosterContextActivity) loadRosterAndPositions(
	ctx context.Context,
	poolID, agentID int32,
	cfg PoolConfig,
) (RosterState, map[int64]sqlcdb.PlayerPosition, []RosterRow, error) {
	logger := activity.GetLogger(ctx)
	rows, err := b.q.ListSimRosterByAgent(ctx, sqlcdb.ListSimRosterByAgentParams{
		PoolID: poolID, AgentID: agentID,
	})
	if err != nil {
		return RosterState{}, nil, nil, fmt.Errorf("simulation: list roster (agent %d): %w", agentID, err)
	}

	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.PlayerID)
	}
	players, err := loadPlayersByIDs(ctx, b.q, ids)
	if err != nil {
		return RosterState{}, nil, nil, fmt.Errorf("simulation: batch load roster (agent %d): %w", agentID, err)
	}

	placements := make(map[int64]RosterSlot, len(rows))
	positions := make(map[int64]sqlcdb.PlayerPosition, len(rows))
	rosterRows := make([]RosterRow, 0, len(rows))

	for _, r := range rows {
		placements[r.PlayerID] = RosterSlot(r.Slot)

		p, ok := players[r.PlayerID]
		if !ok {
			return RosterState{}, nil, nil, fmt.Errorf("simulation: get player %d: not found", r.PlayerID)
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
			NHLPosition: pgconv.FormatPlayerPosition(p.Position),
			Slot:        RosterSlot(r.Slot),
			// The raw team ID; its abbreviation needs a season_teams
			// join (V2 polish).
			Team: pgconv.FormatInt8(p.TeamID),
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
// in round 1 can immediately slot them in round 2 of the same turn. Position
// lookups are batched with one GetPlayersByIDs call (was one GetPlayer per
// candidate not already on the roster).
func (b rosterContextActivity) loadCandidatePositions(
	ctx context.Context,
	playerIDs []int64,
	positions map[int64]sqlcdb.PlayerPosition,
) error {
	need := make([]int64, 0, len(playerIDs))
	for _, id := range playerIDs {
		if _, already := positions[id]; already {
			continue
		}
		need = append(need, id)
	}
	players, err := loadPlayersByIDs(ctx, b.q, need)
	if err != nil {
		return fmt.Errorf("simulation: batch load candidate positions: %w", err)
	}
	for _, id := range need {
		p, ok := players[id]
		if !ok {
			return fmt.Errorf("simulation: get player %d for position catalog: not found", id)
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
// — that's how the LLM identifies its own line in the table. Rows are
// ordered by total roto points descending, agent ID ascending on a tie,
// so the same snapshot always renders the same prompt.
//
// Empty standings (day 1, before any games) → empty slice; valid
// state, no error. A transient DB error is propagated as an error so
// Temporal retries rather than presenting stale/empty standings.
func (b rosterContextActivity) loadStandingsRows(ctx context.Context, poolID, agentID int32) ([]StandingRow, error) {
	latest, err := b.q.GetSimStandingsLatestDate(ctx, poolID)
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
	rows, err := b.q.ListSimStandingsByDate(ctx, sqlcdb.ListSimStandingsByDateParams{
		PoolID: poolID, Date: latest,
	})
	if err != nil {
		return nil, fmt.Errorf("simulation: list standings for date: %w", err)
	}
	snap, err := decodeStandings(rows)
	if err != nil {
		return nil, err
	}

	// Pre-load agent names so the table prints "Sonnet" not "agent 7".
	agents, err := b.q.ListSimAgentsByPool(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("simulation: list agents for standings: %w", err)
	}
	nameByID := make(map[int32]string, len(agents))
	for _, ag := range agents {
		nameByID[ag.ID] = displayName(ag)
	}

	ranks := snap.categoryRanks()
	out := make([]StandingRow, 0, len(snap.agentIDs))
	for _, aid := range snap.agentIDs {
		row := snap.row(aid, ranks)
		row.Agent = nameByID[aid]
		row.IsYou = aid == agentID
		out = append(out, row)
	}
	return out, nil
}

// standingEntry is one agent's stored result in one category: the raw
// stat total the LLM reads as Value, and the roto points UpdateStandings
// awarded for it.
type standingEntry struct {
	value      float64
	rotoPoints float64
}

// standingsSnapshot is one sim_standings date decoded per agent.
type standingsSnapshot struct {
	agentIDs []int32 // total roto points descending, agent ID ascending
	byAgent  map[int32]map[Category]standingEntry
	totals   map[int32]float64
}

// decodeStandings groups the snapshot rows by agent and category and
// orders the agents for the prompt table.
func decodeStandings(rows []sqlcdb.SimStanding) (standingsSnapshot, error) {
	snap := standingsSnapshot{
		byAgent: map[int32]map[Category]standingEntry{},
		totals:  map[int32]float64{},
	}
	for _, r := range rows {
		pts, err := pgconv.NumericToFloat(r.RotoPoints)
		if err != nil {
			return standingsSnapshot{}, fmt.Errorf("simulation: decode roto_points (agent %d, category %q): %w", r.AgentID, r.Category, err)
		}
		val, err := pgconv.NumericToFloat(r.Value)
		if err != nil {
			return standingsSnapshot{}, fmt.Errorf("simulation: decode value (agent %d, category %q): %w", r.AgentID, r.Category, err)
		}
		if _, ok := snap.byAgent[r.AgentID]; !ok {
			snap.byAgent[r.AgentID] = map[Category]standingEntry{}
			snap.agentIDs = append(snap.agentIDs, r.AgentID)
		}
		snap.byAgent[r.AgentID][Category(r.Category)] = standingEntry{value: val, rotoPoints: pts}
		snap.totals[r.AgentID] += pts
	}
	slices.SortFunc(snap.agentIDs, func(x, y int32) int {
		if c := cmp.Compare(snap.totals[y], snap.totals[x]); c != 0 {
			return c
		}
		return cmp.Compare(x, y)
	})
	return snap, nil
}

// categoryRanks ranks the agents in every prompt category, in
// v1FixedCategories order.
//
// The rank comes from the stored roto points, not the value: the points
// already encode the category's direction and GAA's zero-TOI worst-rank
// rule (a zero-TOI agent's stored GAA value is 0, which would sort best).
// Equal values earned equal points, so RankCategory over the points
// (higher is better) reproduces UpdateStandings' competition ranking:
// tied agents share the run's first position ("1, 2, 2, 4"). An agent
// with no row in a category ranks with 0 points.
func (s standingsSnapshot) categoryRanks() map[Category]map[int32]int {
	out := make(map[Category]map[int32]int, len(v1FixedCategories))
	for _, name := range v1FixedCategories {
		cat := Category(name)
		stats := make([]AgentCategoryStat, len(s.agentIDs))
		for i, aid := range s.agentIDs {
			stats[i] = AgentCategoryStat{AgentID: int64(aid), Value: s.byAgent[aid][cat].rotoPoints}
		}
		ranks := make(map[int32]int, len(stats))
		// RankCategory returns its rankings in input order.
		for i, r := range RankCategory(stats, false) {
			ranks[s.agentIDs[i]] = r.Rank
		}
		out[cat] = ranks
	}
	return out
}

// row builds agent aid's prompt row, without its name and is_you flag.
func (s standingsSnapshot) row(aid int32, ranks map[Category]map[int32]int) StandingRow {
	standing := func(cat Category) CategoryStanding {
		return CategoryStanding{
			Value: s.byAgent[aid][cat].value,
			Rank:  float64(ranks[cat][aid]),
		}
	}
	return StandingRow{
		G:            standing(CategoryG),
		A:            standing(CategoryA),
		PlusMinus:    standing(CategoryPM),
		PIM:          standing(CategoryPIM),
		PPP:          standing(CategoryPPP),
		SOG:          standing(CategorySOG),
		W:            standing(CategoryW),
		GA:           standing(CategoryGA),
		GAA:          standing(CategoryGAA),
		TotalRotoPts: s.totals[aid],
	}
}
