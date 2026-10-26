package simulation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

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
//
// A waivers row with no matching players row is a hard error, not the
// silent skip the free-agent loaders use: on-waivers is a working-state
// list (ManageRosterInput.OnWaivers feeds ValidateClaimPlayer), and a
// dropped player must exist in players because it was drafted or added
// from there. The free-agent pool, by contrast, is a large best-effort
// recommendation list where a missing players row is an expected
// cross-table gap. This matches the file's other working-state loaders
// (loadRosterAndPositions, loadAgentPendingClaims, loadCandidatePositions).
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

// dropRosteredFreeAgents removes players already on any pool roster from
// the free-agent pool. BuildFreeAgentPool snapshots the pool once a day,
// before any agent's turn runs; an earlier agent's add saves a roster row
// before a later agent's BuildManageRosterContext queries the roster. A
// player rostered since the snapshot must be dropped so the later agent
// neither sees it in top_free_agents nor passes add_player validation
// against a stale id.
func (b rosterContextActivity) dropRosteredFreeAgents(ctx context.Context, poolID int32, freeAgents []int64) ([]int64, error) {
	if len(freeAgents) == 0 {
		return freeAgents, nil
	}
	rows, err := b.q.ListSimRosterByPool(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("simulation: list roster by pool: %w", err)
	}
	if len(rows) == 0 {
		return freeAgents, nil
	}
	rostered := make(map[int64]struct{}, len(rows))
	for _, r := range rows {
		rostered[r.PlayerID] = struct{}{}
	}
	out := make([]int64, 0, len(freeAgents))
	for _, id := range freeAgents {
		if _, taken := rostered[id]; taken {
			continue
		}
		out = append(out, id)
	}
	return out, nil
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
// skater with a stats row. A skater with zero regular-season games
// before sim_date has no stats row and is intentionally excluded:
// the daily context shows the top N by recent composite (wiki
// puckdb/simulation.md > "Daily context"), and a zero-production
// skater has no composite to rank (it would only add noise below the
// top-20 cutoff). Iteration order of the stats
// rows doesn't matter: RankFreeAgentSkaters re-sorts by composite then
// id, so the final output is deterministic regardless of the query's
// return order.
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
// components and carrying the recent TOI so RankFreeAgentGoalies can
// rank idle goalies below those who played and so the prompt can show
// an idle goalie's zero ice time (its 0.00 recent GAA would otherwise
// look like a shutout). A goalie with zero regular-season games before
// sim_date has no stats row and is intentionally excluded (same
// top-N-by-recent-production rationale as loadFreeAgentSkaters); "idle"
// here means played earlier in the season but not recently, distinct
// from never having played.
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
			RecentTOI: int(r.RecentToi),
		})
	}
	return out, nil
}
