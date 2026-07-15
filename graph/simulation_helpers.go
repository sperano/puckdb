package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/temporal"
	"github.com/sperano/puckdb/worker/simulation"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// timeParseISODate is a thin wrapper around time.Parse so the date
// format is in one place — referenced from parseDateOrError.
func timeParseISODate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// derefInt / derefString unwrap optional GraphQL inputs to the
// value-type the simulation package's typed structs expect. Nil →
// zero value (matches the "field omitted" semantics the JSON config
// used to imply).
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefStopAfterOrDefault returns *p as a validated stop_after value,
// or "never" when *p is nil/empty/invalid. Matches the
// sim_pools.stop_after CHECK constraint default in the schema.
func derefStopAfterOrDefault(p *string) string {
	if p == nil {
		return simulation.StopAfterNever.String()
	}
	stop, err := simulation.ParseStopAfter(*p)
	if err != nil {
		return simulation.StopAfterNever.String()
	}
	return stop.String()
}

// ============================================================================
// Simulation resolver helpers — shared utilities for simulation.resolvers.go.
//
// Lives in a non-gqlgen-managed file so the resolver scaffolding stays
// pristine across regens. Helpers fall into four buckets:
//
//   1. ID conventions (workflow_id mapping)
//   2. sqlcdb → model.* type adapters
//   3. Player-name lookup (N+1 over GetPlayer; acceptable for V1
//      pool sizes — see assemblePlayerNameMap)
//   4. Roto-points aggregation (SUM(roto_points) per agent for the
//      latest standings snapshot)
// ============================================================================

// simPoolWorkflowIDForPool is the canonical workflow-ID convention.
// PoolID → "sim-pool-{id}" — matches the sim_pools.workflow_id
// generated column ('sim-pool-' || id::text), keeping resolver and
// DB in lockstep.
func simPoolWorkflowIDForPool(poolID int32) string {
	return "sim-pool-" + strconv.FormatInt(int64(poolID), 10)
}

// PoolNotFoundError is returned when a sim pool lookup by ID misses.
// Callers (and tests) can use errors.As to distinguish it from
// generic DB / Temporal failures and surface a user-friendly message
// instead of leaking the underlying "no rows in result set".
type PoolNotFoundError struct {
	ID int32
}

func (e *PoolNotFoundError) Error() string {
	return fmt.Sprintf("no pool found with id %d", e.ID)
}

// mapGetSimPoolErr converts a GetSimPool error into either a typed
// PoolNotFoundError (when the row was missing) or a wrapped lookup
// error for anything else.
func mapGetSimPoolErr(err error, poolID int32) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return &PoolNotFoundError{ID: poolID}
	}
	return fmt.Errorf("get sim pool %d: %w", poolID, err)
}

// resolveLatestStandingsDate interprets the (date, err) result of
// GetSimStandingsLatestDate. A real DB error must abort loadSimPool rather
// than be swallowed into an empty-standings result (the previous
// `if err == nil && ...` guard hid connection/query failures as "no
// standings yet"). pgx.ErrNoRows is treated as "no standings yet" — the
// MAX() aggregate normally returns a NULL row rather than ErrNoRows, but
// classifying it defensively keeps an empty pool from erroring.
func resolveLatestStandingsDate(latestDate pgtype.Date, err error) (pgtype.Date, error) {
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.Date{}, nil
		}
		return pgtype.Date{}, err
	}
	return latestDate, nil
}

// loadSimPoolScalar is the read-side path used by simPool, simPools,
// and every cancel/create mutation that returns the post-mutation pool
// state. It reads ONLY the sim_pools scalar columns (one GetSimPool
// query) and defers the expensive nested fields — agents, standings,
// currentDraftAction — to the SimPool field resolvers below, which run
// per pool only when a client actually selects them.
//
// This is what collapses the simPools list to O(1) queries: the list
// resolver runs a single ListSimPools query and decodes each row to a
// scalar model; nested data is never touched unless requested.
func (r *Resolver) loadSimPoolScalar(ctx context.Context, poolID int32) (*model.SimPool, error) {
	pool, err := r.Queries.GetSimPool(ctx, poolID)
	if err != nil {
		return nil, mapGetSimPoolErr(err, poolID)
	}
	return decodeSimPoolBase(pool), nil
}

// latestStandingsSnapshot returns the sim_standings rows for the pool's
// most recent standings date, or an empty slice when the pool has no
// standings yet. Shared by loadSimPoolAgents (for per-agent
// totalRotoPoints) and loadSimPoolStandings (for the pool-level
// standings list) — each resolves independently, so a query selecting
// both fields reads the snapshot once per field.
func (r *Resolver) latestStandingsSnapshot(ctx context.Context, poolID int32) ([]sqlcdb.SimStanding, error) {
	latestDate, err := resolveLatestStandingsDate(r.Queries.GetSimStandingsLatestDate(ctx, poolID))
	if err != nil {
		return nil, fmt.Errorf("get latest standings date for pool %d: %w", poolID, err)
	}
	if !latestDate.Valid {
		return nil, nil
	}
	rows, err := r.Queries.ListSimStandingsByDate(ctx, sqlcdb.ListSimStandingsByDateParams{
		PoolID: poolID,
		Date:   latestDate,
	})
	if err != nil {
		return nil, fmt.Errorf("list latest standings for pool %d: %w", poolID, err)
	}
	return rows, nil
}

// loadSimPoolAgents assembles the pool's agents, each with roster +
// totalRotoPoints. Backs the SimPool.agents field resolver. Query
// count is bounded and independent of agent count: one
// ListSimAgentsByPool, the shared latest-standings snapshot (up to two
// queries), one pool-wide ListSimRosterByPool, and one batched
// GetPlayersByIDs for every referenced player.
func (r *Resolver) loadSimPoolAgents(ctx context.Context, poolID int32) ([]*model.SimAgent, error) {
	agents, err := r.Queries.ListSimAgentsByPool(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("list agents for pool %d: %w", poolID, err)
	}

	// Latest standings snapshot feeds each agent's totalRotoPoints.
	latestStandings, err := r.latestStandingsSnapshot(ctx, poolID)
	if err != nil {
		return nil, err
	}
	standingsByAgent := map[int32][]sqlcdb.SimStanding{}
	for _, s := range latestStandings {
		standingsByAgent[s.AgentID] = append(standingsByAgent[s.AgentID], s)
	}

	// One pool-wide roster read (ordered agent_id, slot, player_id)
	// grouped by agent — avoids a per-agent ListSimRosterByAgent loop.
	// Grouping preserves each agent's (slot, player_id) sub-ordering
	// since the query's own ORDER BY already sorts that way within
	// each agent_id run.
	poolRosters, err := r.Queries.ListSimRosterByPool(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("list roster for pool %d: %w", poolID, err)
	}
	rostersByAgent := map[int32][]sqlcdb.SimRoster{}
	playerIDSet := map[int64]struct{}{}
	for _, row := range poolRosters {
		rostersByAgent[row.AgentID] = append(rostersByAgent[row.AgentID], row)
		playerIDSet[row.PlayerID] = struct{}{}
	}
	players, err := r.assemblePlayerNameMap(ctx, playerIDSet)
	if err != nil {
		return nil, err
	}

	out := make([]*model.SimAgent, 0, len(agents))
	for _, a := range agents {
		ag := decodeSimAgentBase(a)
		ag.Roster = decodeSimRosters(rostersByAgent[a.ID], players)
		ag.TotalRotoPoints = sumRotoPoints(standingsByAgent[a.ID])
		out = append(out, ag)
	}
	return out, nil
}

// loadSimPoolStandings returns the pool's latest standings snapshot.
// Backs the SimPool.standings field resolver. We expose agent IDs
// only; the client resolves agent_id → team_name via the
// SimPool.agents array.
func (r *Resolver) loadSimPoolStandings(ctx context.Context, poolID int32) ([]*model.SimStandingEntry, error) {
	latestStandings, err := r.latestStandingsSnapshot(ctx, poolID)
	if err != nil {
		return nil, err
	}
	return decodeStandings(latestStandings), nil
}

// loadSimPoolCurrentDraftAction computes the pool's current draft
// action. Backs the SimPool.currentDraftAction field resolver. Needs
// the sim_pools row (for status + draft_rounds), the agents (for
// shuffled draft positions), and the completed draft-pick count.
func (r *Resolver) loadSimPoolCurrentDraftAction(ctx context.Context, poolID int32) (*model.CurrentDraftAction, error) {
	pool, err := r.Queries.GetSimPool(ctx, poolID)
	if err != nil {
		return nil, mapGetSimPoolErr(err, poolID)
	}
	agents, err := r.Queries.ListSimAgentsByPool(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("list agents for pool %d: %w", poolID, err)
	}
	completedDraftPicks, err := r.Queries.CountSimDraftPicks(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("count draft picks for pool %d: %w", poolID, err)
	}
	return currentDraftAction(pool, agents, completedDraftPicks), nil
}

// currentDraftAction returns the next draft pick's round/pick/agent
// when the pool is in draft status. Nil when the pool isn't drafting
// or the draft is already complete. Pure function over the inputs.
//
// Three cases when status=draft:
//   - Pre-shuffle (no agent has draft_position yet): Round 1, Pick 1,
//     TotalPicks = rounds × len(agents), AgentID = 0 (sentinel — the
//     CLI renders this as "draft order not yet assigned" so the
//     operator sees the draft size before the workflow shuffles).
//   - Mid-draft: Round/Pick computed from `completed`, AgentID/Name
//     resolved through the shuffled draft order.
//   - Post-draft (completed >= totalPicks): returns nil so the caller
//     stops showing a "currently picking" line.
//
// Snake-draft order: in round R, agent at draft_position P picks at
// pick-in-round P when R is odd, or (teams-P+1) when R is even.
func currentDraftAction(pool sqlcdb.SimPool, agents []sqlcdb.SimAgent, completed int64) *model.CurrentDraftAction {
	if pool.Status != string(simulation.PoolStatusDraft) {
		return nil
	}
	ordered := make([]sqlcdb.SimAgent, 0, len(agents))
	for _, a := range agents {
		if a.DraftPosition.Valid {
			ordered = append(ordered, a)
		}
	}
	rounds := int(pool.DraftRounds)
	if len(ordered) == 0 {
		// Pre-shuffle: surface the draft size so the operator sees
		// "Round 1, Pick 1 of N" before workflow has assigned positions.
		if len(agents) == 0 {
			return nil
		}
		return &model.CurrentDraftAction{
			Round:      1,
			Pick:       1,
			TotalPicks: rounds * len(agents),
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].DraftPosition.Int32 < ordered[j].DraftPosition.Int32
	})

	teams := len(ordered)
	totalPicks := rounds * teams
	if int(completed) >= totalPicks {
		return nil
	}

	pickNumber := int(completed) + 1
	round := ((pickNumber - 1) / teams) + 1
	pickInRound := ((pickNumber - 1) % teams) + 1

	idx := pickInRound - 1
	if round%2 == 0 {
		idx = teams - pickInRound
	}
	a := ordered[idx]
	return &model.CurrentDraftAction{
		Round:      round,
		Pick:       pickInRound,
		TotalPicks: totalPicks,
		AgentID:    int(a.ID),
	}
}

// assemblePlayerNameMap loads display data for every player_id in
// the set and returns id → playerSummary. Batched into one
// GetPlayersByIDs call (was one GetPlayer per id).
//
// Empty input → empty map (no DB calls).
func (r *Resolver) assemblePlayerNameMap(ctx context.Context, ids map[int64]struct{}) (map[int64]playerSummary, error) {
	if len(ids) == 0 {
		return map[int64]playerSummary{}, nil
	}
	idList := make([]int64, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	players, err := r.Queries.GetPlayersByIDs(ctx, idList)
	if err != nil {
		return nil, fmt.Errorf("get players by ids: %w", err)
	}
	out := make(map[int64]playerSummary, len(players))
	for _, p := range players {
		out[p.ID] = playerSummary{
			Name:        p.FirstName + " " + p.LastName,
			NHLPosition: nullPositionString(p.Position),
			NHLTeamID:   nullTeamIDString(p.TeamID),
		}
	}
	// WHERE id = ANY($1) silently drops ids with no matching row —
	// unlike the old per-id GetPlayer, it can't surface a "no rows"
	// error on its own. Every id here comes off a sim_rosters row
	// (FK'd to players), so a miss means a data-integrity gap; match
	// the old fail-fast behavior rather than rendering a blank name.
	for id := range ids {
		if _, ok := out[id]; !ok {
			return nil, fmt.Errorf("get player %d: not found", id)
		}
	}
	return out, nil
}

// playerSummary is the resolver-side cache row for one player.
// Stored in a map by player_id; produced once per resolver call.
type playerSummary struct {
	Name        string
	NHLPosition string
	NHLTeamID   string
}

// nullPositionString unwraps the nullable position enum into a
// display string; empty when null.
func nullPositionString(p sqlcdb.NullPlayerPosition) string {
	if !p.Valid {
		return ""
	}
	return string(p.PlayerPosition)
}

// nullTeamIDString turns a nullable Int8 team_id into a string for
// display. V1 returns just the ID; a future enhancement would join
// season_teams to surface the abbrev — but that needs the pool's
// season for the season_teams composite key, which is more plumbing
// than V1 warrants.
func nullTeamIDString(t pgtype.Int8) string {
	if !t.Valid {
		return ""
	}
	return strconv.FormatInt(t.Int64, 10)
}

// sumRotoPoints aggregates an agent's standings rows into one float
// — the headline "total roto points" the dashboard shows. Handles
// the GAA category's NaN-protection by skipping invalid Numerics.
func sumRotoPoints(rows []sqlcdb.SimStanding) float64 {
	total := 0.0
	for _, r := range rows {
		f, err := simulation.NumericToFloat(r.RotoPoints)
		if err != nil {
			continue
		}
		total += f
	}
	return total
}

// numericFromFloat is the inverse of numericToFloat — used by
// createSimPoolImpl when projecting GraphQL input floats onto the
// pgtype.Numeric columns sim_pools.max_llm_cost_usd_per_pool and
// sim_agents.temperature.
func numericFromFloat(f float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(f, 'f', 6, 64)); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

// flattenRosterPositions turns the GraphQL list-of-{slot,count}
// shape into a map keyed by RosterSlot. GraphQL has no map type, so
// the wire format is a list; the resolver flattens here at the
// boundary. Slots not present in the input default to 0 in the map
// lookup (`rosters[SlotC]` returns the int zero value).
func flattenRosterPositions(input []*model.SimRosterPositionInput) map[simulation.RosterSlot]int {
	out := make(map[simulation.RosterSlot]int, len(input))
	for _, e := range input {
		out[simulation.RosterSlot(e.Slot)] = e.Count
	}
	return out
}

// ============================================================================
// sqlcdb → model.* adapters
// ============================================================================

// decodeSimPoolBase produces the scalar-only SimPool. Callers
// populate Agents / Standings from separate queries.
func decodeSimPoolBase(p sqlcdb.SimPool) *model.SimPool {
	cost, _ := simulation.NumericToFloat(p.TotalLLMCostUSD)
	out := &model.SimPool{
		ID:              int(p.ID),
		Name:            p.Name,
		Season:          int(p.Season),
		Status:          p.Status,
		TotalLlmCostUsd: cost,
		StopAfter:       p.StopAfter,
		MaxSeasonDays:   int(p.MaxSeasonDays),
	}
	if p.SimDate.Valid {
		s := p.SimDate.Time.Format("2006-01-02")
		out.SimDate = &s
	}
	return out
}

// decodeSimAgentBase produces a SimAgent with scalar fields populated
// — Roster + TotalRotoPoints come from later queries.
func decodeSimAgentBase(a sqlcdb.SimAgent) *model.SimAgent {
	out := &model.SimAgent{
		ID:       int(a.ID),
		TeamName: a.TeamName,
		Provider: a.Provider,
		Model:    a.Model,
	}
	if a.DraftPosition.Valid {
		dp := int(a.DraftPosition.Int32)
		out.DraftPosition = &dp
	}
	return out
}

// decodeSimRosters maps a slice of sqlcdb.SimRoster + the prefetched
// player map to a slice of resolver-shaped SimRosterEntry.
func decodeSimRosters(rows []sqlcdb.SimRoster, players map[int64]playerSummary) []*model.SimRosterEntry {
	out := make([]*model.SimRosterEntry, 0, len(rows))
	for _, r := range rows {
		p := players[r.PlayerID]
		out = append(out, &model.SimRosterEntry{
			PlayerID:    int(r.PlayerID),
			PlayerName:  p.Name,
			Slot:        r.Slot,
			NhlPosition: p.NHLPosition,
			NhlTeam:     p.NHLTeamID,
		})
	}
	return out
}

// decodeStandings flattens sqlcdb.SimStanding rows into the
// SimStandingEntry shape. Sorted by (agent_id, category) for stable
// rendering — the client joins agent_id → team_name from the
// SimPool.agents array it fetches in the same response.
func decodeStandings(rows []sqlcdb.SimStanding) []*model.SimStandingEntry {
	out := make([]*model.SimStandingEntry, 0, len(rows))
	for _, s := range rows {
		val, _ := simulation.NumericToFloat(s.Value)
		rp, _ := simulation.NumericToFloat(s.RotoPoints)
		out = append(out, &model.SimStandingEntry{
			AgentID:    int(s.AgentID),
			Category:   s.Category,
			Value:      val,
			RotoPoints: rp,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AgentID != out[j].AgentID {
			return out[i].AgentID < out[j].AgentID
		}
		return out[i].Category < out[j].Category
	})
	return out
}

// decodeSimTransactions assembles the full SimTransaction list,
// including per-tx playerName / dropPlayerName lookups and the
// joined sim_lineup_moves child rows for lineup_set transactions.
//
// Two passes:
//  1. Collect every player ID referenced (player_id, drop_player_id,
//     plus per-move player_id and displaced_player_id).
//  2. Single name-lookup batch, then build resolver rows.
func (r *Resolver) decodeSimTransactions(ctx context.Context, txs []sqlcdb.SimTransaction) ([]*model.SimTransaction, error) {
	playerIDs := map[int64]struct{}{}
	movesByTx := map[int32][]sqlcdb.SimLineupMove{}
	for _, t := range txs {
		if t.PlayerID.Valid {
			playerIDs[t.PlayerID.Int64] = struct{}{}
		}
		if t.DropPlayerID.Valid {
			playerIDs[t.DropPlayerID.Int64] = struct{}{}
		}
		if t.Type == string(simulation.TransactionTypeLineupSet) {
			moves, err := r.Queries.ListSimLineupMovesByTransaction(ctx, t.ID)
			if err != nil {
				return nil, fmt.Errorf("list lineup moves for tx %d: %w", t.ID, err)
			}
			movesByTx[t.ID] = moves
			for _, m := range moves {
				playerIDs[m.PlayerID] = struct{}{}
				if m.DisplacedPlayerID.Valid {
					playerIDs[m.DisplacedPlayerID.Int64] = struct{}{}
				}
			}
		}
	}
	players, err := r.assemblePlayerNameMap(ctx, playerIDs)
	if err != nil {
		return nil, err
	}

	out := make([]*model.SimTransaction, 0, len(txs))
	for _, t := range txs {
		row := &model.SimTransaction{
			ID:        int(t.ID),
			AgentID:   int(t.AgentID),
			Date:      formatPgDate(t.Date),
			Type:      t.Type,
			Reasoning: t.Reasoning,
		}
		if t.PlayerID.Valid {
			name := players[t.PlayerID.Int64].Name
			row.PlayerName = ptrStringIfNotEmpty(name)
		}
		if t.DropPlayerID.Valid {
			name := players[t.DropPlayerID.Int64].Name
			row.DropPlayerName = ptrStringIfNotEmpty(name)
		}
		if t.Round.Valid {
			v := int(t.Round.Int32)
			row.Round = &v
		}
		if t.Pick.Valid {
			v := int(t.Pick.Int32)
			row.Pick = &v
		}
		if t.ErrorKind.Valid {
			s := t.ErrorKind.String
			row.ErrorKind = &s
		}
		if t.ErrorDetail.Valid {
			s := t.ErrorDetail.String
			row.ErrorDetail = &s
		}
		if t.CostUSD.Valid {
			f, _ := simulation.NumericToFloat(t.CostUSD)
			row.CostUsd = &f
		}
		if t.CapUSD.Valid {
			f, _ := simulation.NumericToFloat(t.CapUSD)
			row.CapUsd = &f
		}
		row.LineupMoves = decodeLineupMoves(movesByTx[t.ID], players)
		out = append(out, row)
	}
	return out, nil
}

// decodeLineupMoves maps sim_lineup_moves rows to resolver shape.
// displaced_player_id is nullable — empty pgtype.Int8 → nil
// displacedPlayerName.
func decodeLineupMoves(rows []sqlcdb.SimLineupMove, players map[int64]playerSummary) []*model.SimLineupMove {
	out := make([]*model.SimLineupMove, 0, len(rows))
	for _, m := range rows {
		row := &model.SimLineupMove{
			Sequence:   int(m.Sequence),
			PlayerName: players[m.PlayerID].Name,
			FromSlot:   m.FromSlot,
			ToSlot:     m.ToSlot,
		}
		if m.DisplacedPlayerID.Valid {
			name := players[m.DisplacedPlayerID.Int64].Name
			row.DisplacedPlayerName = ptrStringIfNotEmpty(name)
		}
		out = append(out, row)
	}
	return out
}

// formatPgDate returns "2006-01-02" or empty string for invalid Dates.
// Used for SimTransaction.date and SimPool.simDate display.
func formatPgDate(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}

// parseDateOrError parses a "2006-01-02" date string into pgtype.Date.
// Empty input returns the zero (Valid=false) date with no error so
// callers can distinguish "no filter" from "bad input".
func parseDateOrError(s string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{}, nil
	}
	t, err := timeParseISODate(s)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("invalid date %q (expected YYYY-MM-DD): %w", s, err)
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}

// defaultSimTxLimit caps simTransactions queries that omit the
// limit arg. Plenty for a dashboard log panel; clients that
// genuinely need more pass an explicit limit.
const defaultSimTxLimit = 100

// groupStandingsByDate groups a flat ListSimStandingsByPool result
// into one inner slice per distinct date, oldest day first. Within
// a day's slice, decodeStandings handles the (agent, category)
// sort ordering.
func groupStandingsByDate(rows []sqlcdb.SimStanding) [][]*model.SimStandingEntry {
	// Bucket by date string for stable ordering — pgtype.Date isn't
	// directly comparable in a map key, but the formatted date is.
	byDate := map[string][]sqlcdb.SimStanding{}
	dates := []string{}
	for _, r := range rows {
		key := formatPgDate(r.Date)
		if _, ok := byDate[key]; !ok {
			dates = append(dates, key)
		}
		byDate[key] = append(byDate[key], r)
	}
	sort.Strings(dates)

	out := make([][]*model.SimStandingEntry, 0, len(dates))
	for _, d := range dates {
		out = append(out, decodeStandings(byDate[d]))
	}
	return out
}

// ============================================================================
// CreateSimPool — pool insert + workflow start
// ============================================================================

// createSimPoolImpl is the createSimPool mutation's behavior split
// out from the resolver method so it stays unit-testable on a
// stub Resolver without running the gqlgen executor.
//
// Steps in order:
//  1. In one DB transaction: INSERT the sim_pools row (the DB
//     auto-generates id and the workflow_id column
//     'sim-pool-' || id::text, so no extra UPDATE is needed) plus one
//     sim_agents row per agent. Wrapping these in a transaction means a
//     mid-insert failure can't leave an orphaned pool with a partial
//     agent set.
//  2. Only AFTER the commit, start the SimPoolWorkflow with
//     WorkflowID = 'sim-pool-{id}' and WorkflowIDReusePolicy =
//     REJECT_DUPLICATE so retried mutations don't double-launch.
//
// If the workflow start fails after the commit, the pool row is already
// durable; leaving it in 'draft' would look like a pool that is about to
// draft but has no workflow driving it. We best-effort flip it to
// 'cancelled' (an existing terminal status) so operators see it is inert.
func (r *Resolver) createSimPoolImpl(ctx context.Context, input model.CreateSimPoolInput) (*model.SimPool, error) {
	if r.DB == nil {
		return nil, errDatabaseNotConfigured
	}
	rosters := flattenRosterPositions(input.RosterPositions)
	capNum, err := numericFromFloat(input.MaxLlmCostUsdPerPool)
	if err != nil {
		return nil, fmt.Errorf("encode max_llm_cost_usd_per_pool: %w", err)
	}

	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin sim pool tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed
	q := r.Queries.WithTx(tx)

	pool, err := q.InsertSimPool(ctx, sqlcdb.InsertSimPoolParams{
		Name:                 input.Name,
		Season:               int32(input.Season),
		Status:               string(simulation.PoolStatusDraft),
		NumTeams:             int32(len(input.Agents)),
		WaiverDays:           int32(input.WaiverDays),
		DraftRounds:          int32(input.DraftRounds),
		MaxLLMCostUsdPerPool: capNum,
		Categories:           append([]string(nil), input.Categories...),
		RosterC:              int32(rosters[simulation.SlotC]),
		RosterLW:             int32(rosters[simulation.SlotLW]),
		RosterRW:             int32(rosters[simulation.SlotRW]),
		RosterD:              int32(rosters[simulation.SlotD]),
		RosterG:              int32(rosters[simulation.SlotG]),
		RosterUtil:           int32(rosters[simulation.SlotUtil]),
		RosterBN:             int32(rosters[simulation.SlotBN]),
		RosterIR:             int32(rosters[simulation.SlotIR]),
		StopAfter:            derefStopAfterOrDefault(input.StopAfter),
		MaxSeasonDays:        int32(derefInt(input.MaxSeasonDays)),
	})
	if err != nil {
		return nil, fmt.Errorf("insert sim pool: %w", err)
	}

	// Insert one sim_agents row per agent. draft_position is the
	// 1-indexed position in the input list — a stable per-agent
	// identity used for tie-breaks and display. The actual
	// Agents insert without draft_position — that column captures
	// the SideEffect-shuffled order, written by the workflow's
	// RecordDraftOrder activity. It stays NULL until then.
	for i, a := range input.Agents {
		var temperature pgtype.Numeric
		if a.Temperature != nil {
			t, err := numericFromFloat(*a.Temperature)
			if err != nil {
				return nil, fmt.Errorf("encode agent #%d temperature: %w", i, err)
			}
			temperature = t
		}
		if _, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
			PoolID:         pool.ID,
			Provider:       strings.ToLower(a.Provider),
			Model:          a.Model,
			Strategy:       a.Strategy,
			TimeoutSeconds: int32(derefInt(a.TimeoutSeconds)),
			Temperature:    temperature,
			APIBase:        derefString(a.APIBase),
			MaxTokens:      int32(derefInt(a.MaxTokens)),
		}); err != nil {
			return nil, fmt.Errorf("insert sim agent #%d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit sim pool: %w", err)
	}

	// Pool + agents are now durable. Start the workflow that drives the
	// pool; only reachable after a successful commit so we never launch a
	// workflow against a half-written pool.
	wfID := simPoolWorkflowIDForPool(pool.ID)
	opts := client.StartWorkflowOptions{
		ID:                    wfID,
		TaskQueue:             temporal.QueueTasks,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}
	if _, err := r.TemporalClient.ExecuteWorkflow(ctx, opts, simulation.SimPoolWorkflow,
		simulation.SimPoolWorkflowInput{PoolID: pool.ID}); err != nil {
		// The pool row is already committed. Mark it cancelled (best
		// effort, outside the now-committed tx) so it doesn't linger in
		// 'draft' with no workflow behind it.
		if markErr := r.Queries.UpdateSimPoolStatus(ctx, sqlcdb.UpdateSimPoolStatusParams{
			ID:     pool.ID,
			Status: string(simulation.PoolStatusCancelled),
		}); markErr != nil {
			return nil, fmt.Errorf("start sim pool workflow %s: %w (and failed to mark pool %d cancelled: %v)", wfID, err, pool.ID, markErr)
		}
		return nil, fmt.Errorf("start sim pool workflow %s (pool %d marked cancelled): %w", wfID, pool.ID, err)
	}

	return r.loadSimPoolScalar(ctx, pool.ID)
}
