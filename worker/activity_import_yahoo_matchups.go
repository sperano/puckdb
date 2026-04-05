package worker

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
)

// importYahooMatchups reads cached matchup data for all weeks and upserts to the database.
func (a *SeasonsActivities) importYahooMatchups(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	var totalImported int

	for week := 1; week <= maxMatchupWeeks; week++ {
		res := resource.Matchups{Season: input.Season, LeagueID: input.LeagueID, Week: week}
		if !a.Storage.Exists(res.Path()) {
			break // No more weeks cached
		}

		fantasy, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, res)
		if err != nil {
			return totalImported, fmt.Errorf("read matchups week %d cache: %w", week, err)
		}

		matchups := fantasy.League.Scoreboard.Matchups.Slice
		if len(matchups) == 0 {
			continue
		}

		params := make([]sqlcdb.UpsertYahooMatchupBatchParams, 0, len(matchups))
		for _, m := range matchups {
			if len(m.Teams.Slice) != 2 {
				continue // Skip malformed matchups
			}

			team1 := m.Teams.Slice[0]
			team2 := m.Teams.Slice[1]

			params = append(params, sqlcdb.UpsertYahooMatchupBatchParams{
				LeagueID:      int32(input.LeagueID),
				Week:          int32(m.Week),
				Team1ID:       int32(team1.TeamID),
				Team2ID:       int32(team2.TeamID),
				Team1Points:   pgtype.Float4{Float32: float32(team1.TeamPoints.Total), Valid: true},
				Team2Points:   pgtype.Float4{Float32: float32(team2.TeamPoints.Total), Valid: true},
				Status:        pgtype.Text{String: m.Status, Valid: m.Status != ""},
				IsPlayoffs:    m.IsPlayoffs != 0,
				IsConsolation: m.IsConsolation != 0,
			})
		}

		if len(params) == 0 {
			continue
		}

		if err := execBatch(a.ImportQueries.UpsertYahooMatchupBatch(ctx, params), func(i int) string {
			return fmt.Sprintf("matchup week %d team1 %d vs team2 %d", params[i].Week, params[i].Team1ID, params[i].Team2ID)
		}); err != nil {
			return totalImported, err
		}
		totalImported += len(params)
	}

	return totalImported, nil
}
