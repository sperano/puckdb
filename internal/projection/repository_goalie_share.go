package projection

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// loadGoalieStarts reads the clubs' most recent started games on or before
// cutoff for the goalie start share, over the lookback window. Models
// without the share, or with an empty window, load none.
func (r *Repository) loadGoalieStarts(ctx context.Context, cfg Config, targetSeason int, cutoff pgtype.Date) ([]GoalieStart, error) {
	if !supportsGoalieStartShare(cfg.ModelVersion) || cfg.GoalieShareWindowGames <= 0 {
		return nil, nil
	}
	rows, err := r.queries.ListProjectionGoalieRecentStarts(ctx, sqlcdb.ListProjectionGoalieRecentStartsParams{
		WindowGames:  int32(cfg.GoalieShareWindowGames),
		TargetSeason: int32(targetSeason),
		MinSeason:    int32(historyFloorSeason(targetSeason, cfg.LookbackSeasons)),
		Cutoff:       cutoff,
	})
	if err != nil {
		return nil, fmt.Errorf("load goalie recent starts: %w", err)
	}
	out := make([]GoalieStart, 0, len(rows))
	for _, row := range rows {
		out = append(out, GoalieStart{
			TeamID: row.TeamID, PlayerID: row.PlayerID, Season: int(row.Season),
			GameDate: optionalDate(row.GameDate), Playoff: row.GameType == sqlcdb.GameTypePlayoffs,
			RecencyRank: int(row.RecencyRank), RegularSeasonRank: int(row.RegularSeasonRank),
		})
	}
	return out, nil
}

// loadEvaluationTargetTeamsWithGoalies adds each goalie's first club of a
// held-out season (by his first appearance) to the skaters' clubs, so the
// goalie start share can be backtested. Only nhl-baseline-v7 and later read
// it: the published versions' evaluation-data hashes cover the skater clubs
// alone.
func (r *Repository) loadEvaluationTargetTeamsWithGoalies(ctx context.Context, targetSeason int) ([]PlayerTeam, error) {
	out, err := r.loadEvaluationTargetTeams(ctx, targetSeason)
	if err != nil {
		return nil, err
	}
	rows, err := r.queries.ListProjectionEvaluationGoalieTargetTeams(ctx, int32(targetSeason))
	if err != nil {
		return nil, fmt.Errorf("load evaluation goalie target teams: %w", err)
	}
	for _, row := range rows {
		out = append(out, PlayerTeam{PlayerID: row.PlayerID, TeamID: row.TeamID})
	}
	return out, nil
}
