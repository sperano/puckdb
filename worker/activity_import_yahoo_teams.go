package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ImportYahooTeamsInput contains parameters for importing Yahoo teams.
type ImportYahooTeamsInput struct {
	Season int
	Teams  []TeamInfo
}

// ImportYahooTeamsResult contains the results of importing Yahoo teams.
type ImportYahooTeamsResult struct {
	TeamsImported    int
	ManagersImported int
}

// ImportYahooTeams imports Yahoo teams from cached XML into the database.
func (a *SeasonsActivities) ImportYahooTeams(ctx context.Context, input ImportYahooTeamsInput) (ImportYahooTeamsResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportYahooTeams", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	result := ImportYahooTeamsResult{}

	if len(input.Teams) == 0 {
		logger.Warn("No teams to import", "season", input.Season)
		return result, nil
	}

	// Collect all team and manager params
	var teamParams []sqlcdb.UpsertYahooTeamBatchParams
	var managerParams []sqlcdb.UpsertYahooTeamManagerBatchParams

	for _, teamInfo := range input.Teams {
		// Read the team file
		teamRes := resource.Team{Season: input.Season, LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID}
		if !a.Storage.Exists(teamRes.Path()) {
			logger.Warn("No team file found in cache",
				"season", input.Season,
				"leagueID", teamInfo.LeagueID,
				"teamID", teamInfo.TeamID)
			continue
		}

		fantasy, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, teamRes)
		if err != nil {
			return result, fmt.Errorf("read team file %d: %w", teamInfo.TeamID, err)
		}

		team := fantasy.Team

		// Extract logo URL (prefer large size)
		var logoURL string
		for _, logo := range team.TeamLogos.Slice {
			if logo.Size == "large" || logoURL == "" {
				logoURL = logo.URL
			}
		}

		teamParams = append(teamParams, sqlcdb.UpsertYahooTeamBatchParams{
			LeagueID:              int32(teamInfo.LeagueID),
			ID:                    int32(team.ID),
			TeamKey:               team.Key,
			Name:                  team.Name,
			Url:                   team.URL,
			LogoUrl:               logoURL,
			DraftPosition:         pgtype.Int4{Int32: int32(team.DraftPosition), Valid: team.DraftPosition > 0},
			WaiverPriority:        pgtype.Int4{Int32: int32(team.WaiverPriority), Valid: team.WaiverPriority > 0},
			NumberOfMoves:         int32(team.NumberOfMoves),
			NumberOfTrades:        int32(team.NumberOfTrades),
			IsOwnedByCurrentLogin: team.IsOwnedByCurrentLogin,
		})

		// Collect managers for this team
		for _, manager := range team.Managers.Slice {
			managerParams = append(managerParams, sqlcdb.UpsertYahooTeamManagerBatchParams{
				LeagueID:       int32(teamInfo.LeagueID),
				TeamID:         int32(team.ID),
				ID:             int32(manager.ID),
				Nickname:       manager.Nickname,
				Guid:           manager.GUID,
				Email:          manager.EMail,
				ImageUrl:       manager.ImageURL,
				FeloScore:      int32(manager.FeloScore),
				FeloTier:       manager.FeloTier,
				IsCurrentLogin: manager.IsCurrentLogin,
				IsCommissioner: false, // Not in the XML response, would need separate lookup
			})
		}
	}

	// Batch upsert teams
	if len(teamParams) > 0 {
		var batchErr error
		results := a.ImportQueries.UpsertYahooTeamBatch(ctx, teamParams)
		results.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				batchErr = fmt.Errorf("team %d: %w", teamParams[i].ID, err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.TeamsImported = len(teamParams)
	}

	// Batch upsert managers
	if len(managerParams) > 0 {
		var batchErr error
		results := a.ImportQueries.UpsertYahooTeamManagerBatch(ctx, managerParams)
		results.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				batchErr = fmt.Errorf("manager %d (team %d): %w", managerParams[i].ID, managerParams[i].TeamID, err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.ManagersImported = len(managerParams)
	}

	logger.Info("Imported Yahoo teams",
		"season", input.Season,
		"teams", result.TeamsImported,
		"managers", result.ManagersImported)

	return result, nil
}

// YahooTeamUpserter is the interface for database operations needed by Yahoo team import.
type YahooTeamUpserter interface {
	UpsertYahooTeamBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamBatchParams) *sqlcdb.UpsertYahooTeamBatchBatchResults
	UpsertYahooTeamManagerBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamManagerBatchParams) *sqlcdb.UpsertYahooTeamManagerBatchBatchResults
}
