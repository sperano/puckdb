package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// PlayerCareerUpserter is the interface for player career database operations.
type PlayerCareerUpserter interface {
	UpsertPlayerAwardBatch(ctx context.Context, arg []sqlcdb.UpsertPlayerAwardBatchParams) *sqlcdb.UpsertPlayerAwardBatchBatchResults
	UpsertPlayerSeasonTotalBatch(ctx context.Context, arg []sqlcdb.UpsertPlayerSeasonTotalBatchParams) *sqlcdb.UpsertPlayerSeasonTotalBatchBatchResults
}

// ImportPlayerCareerDataResult contains the results of importing player career data.
type ImportPlayerCareerDataResult struct {
	PlayersProcessed int `json:"playersProcessed"`
	AwardsImported   int `json:"awardsImported"`
	TotalsImported   int `json:"totalsImported"`
}

// ImportPlayerCareerData imports player awards and season totals from cached PlayerLanding files.
func (a *SeasonsActivities) ImportPlayerCareerData(ctx context.Context) (ImportPlayerCareerDataResult, error) {
	logger := activity.GetLogger(ctx)
	result := ImportPlayerCareerDataResult{}

	names, err := a.Storage.List("players", "json")
	if err != nil {
		return result, fmt.Errorf("list player landing files: %w", err)
	}
	if len(names) == 0 {
		logger.Warn("No player landing files found in cache")
	}

	var awardParams []sqlcdb.UpsertPlayerAwardBatchParams
	var totalParams []sqlcdb.UpsertPlayerSeasonTotalBatchParams

	for _, name := range names {
		playerID, ok := parsePlayerLandingID(name)
		if !ok {
			continue
		}

		landingRes := resource.PlayerLanding{PlayerID: nhl.NewPlayerID(playerID)}
		landing, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, landingRes)
		if err != nil {
			logger.Debug("Failed to read player landing", "playerID", playerID, "error", err)
			continue
		}

		for _, award := range landing.Awards {
			trophyName := award.Trophy.Default
			for _, awardSeason := range award.Seasons {
				awardParams = append(awardParams, sqlcdb.UpsertPlayerAwardBatchParams{
					PlayerID:   playerID,
					TrophyName: trophyName,
					Season:     int32(awardSeason.SeasonID.StartYear()),
				})
			}
		}

		for _, st := range landing.SeasonTotals {
			var sequence int32
			if st.Sequence != nil {
				sequence = int32(*st.Sequence)
			}

			totalParams = append(totalParams, sqlcdb.UpsertPlayerSeasonTotalBatchParams{
				PlayerID:     playerID,
				Season:       int32(st.Season.StartYear()),
				GameType:     int16(st.GameType.Int()),
				LeagueAbbrev: st.LeagueAbbrev,
				TeamName:     st.TeamName.Default,
				Sequence:     sequence,
				GamesPlayed:  int32(st.GamesPlayed),
				Goals:        intPtrToInt4(st.Goals),
				Assists:      intPtrToInt4(st.Assists),
				Points:       intPtrToInt4(st.Points),
				PlusMinus:    intPtrToInt4(st.PlusMinus),
				PIM:          intPtrToInt4(st.PIM),
			})
		}

		result.PlayersProcessed++
	}

	if len(awardParams) > 0 {
		var batchErr error
		aResults := a.ImportQueries.UpsertPlayerAwardBatch(ctx, awardParams)
		aResults.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				p := awardParams[i]
				batchErr = fmt.Errorf("award player %d trophy %q season %d: %w", p.PlayerID, p.TrophyName, p.Season, err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.AwardsImported = len(awardParams)
	}

	if len(totalParams) > 0 {
		var batchErr error
		tResults := a.ImportQueries.UpsertPlayerSeasonTotalBatch(ctx, totalParams)
		tResults.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				p := totalParams[i]
				batchErr = fmt.Errorf("season total player %d season %d league %s: %w", p.PlayerID, p.Season, p.LeagueAbbrev, err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.TotalsImported = len(totalParams)
	}

	logger.Info("Imported player career data",
		"players", result.PlayersProcessed,
		"awards", result.AwardsImported,
		"totals", result.TotalsImported)

	return result, nil
}

// parsePlayerLandingID extracts a player ID from a filename like "player-8478402-landing".
// Returns the player ID and true on success, or 0 and false if the name does not match.
func parsePlayerLandingID(name string) (int64, bool) {
	parts := strings.Split(name, "-")
	if len(parts) < 3 || parts[0] != "player" || parts[len(parts)-1] != "landing" {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// intPtrToInt4 converts a *int to pgtype.Int4.
func intPtrToInt4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}
