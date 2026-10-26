package yahoo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportActivities holds dependencies for Yahoo data import activities.
// Tx runs the writes that must be atomic, such as all the rows of one date
// in ImportYahooDataForDate.
type ImportActivities struct {
	Storage  store.Storage
	GobCache *cache.GobCache
	Queries  YahooImportQueries
	Tx       Transactor
}

// ImportYahooTeams imports Yahoo teams from cached XML into the database.
func (a *ImportActivities) ImportYahooTeams(ctx context.Context, input ImportYahooTeamsInput) (ImportYahooTeamsResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooTeams")()

	logger := activity.GetLogger(ctx)
	result := ImportYahooTeamsResult{}

	if len(input.Teams) == 0 {
		logger.Warn("No teams to import", "season", input.Season)
		return result, nil
	}
	for _, teamInfo := range input.Teams {
		teamRes := resource.Team{Season: input.Season, LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID}
		if !resource.Exists(ctx, a.Storage, teamRes) {
			return result, fmt.Errorf("required Yahoo team cache is missing for season %d league %d team %d",
				input.Season, teamInfo.LeagueID, teamInfo.TeamID)
		}
	}

	// Collect all team and manager params
	var teamParams []sqlcdb.UpsertYahooTeamBatchParams
	var managerParams []sqlcdb.UpsertYahooTeamManagerBatchParams

	for _, teamInfo := range input.Teams {
		// Read the team file
		teamRes := resource.Team{Season: input.Season, LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID}
		fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, teamRes)
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
		if err := shared.ExecBatch(a.Queries.UpsertYahooTeamBatch(ctx, teamParams), func(i int) string {
			return fmt.Sprintf("team %d", teamParams[i].ID)
		}); err != nil {
			return result, err
		}
		result.TeamsImported = len(teamParams)
	}

	// Batch upsert managers
	if len(managerParams) > 0 {
		if err := shared.ExecBatch(a.Queries.UpsertYahooTeamManagerBatch(ctx, managerParams), func(i int) string {
			return fmt.Sprintf("manager %d (team %d)", managerParams[i].ID, managerParams[i].TeamID)
		}); err != nil {
			return result, err
		}
		result.ManagersImported = len(managerParams)
	}

	logger.Info("Imported Yahoo teams",
		"season", input.Season,
		"teams", result.TeamsImported,
		"managers", result.ManagersImported)

	return result, nil
}
