package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// ImportYahooTeamsInput contains parameters for importing Yahoo teams.
type ImportYahooTeamsInput struct {
	Season   int
	LeagueID int
	Teams    []TeamInfo
}

// ImportYahooTeamsResult contains the results of importing Yahoo teams.
type ImportYahooTeamsResult struct {
	TeamsImported    int
	ManagersImported int
}

// ImportYahooTeamsActivity imports Yahoo teams from cached XML into the database.
func ImportYahooTeamsActivity(ctx context.Context, input ImportYahooTeamsInput) (ImportYahooTeamsResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportYahooTeamsActivity", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	fs := store.NewStore()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return ImportYahooTeamsResult{}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	result, err := importYahooTeamsImpl(ctx, fs, queries, input)
	if err != nil {
		return result, err
	}

	logger.Info("Imported Yahoo teams",
		"season", input.Season,
		"leagueID", input.LeagueID,
		"teams", result.TeamsImported,
		"managers", result.ManagersImported)

	return result, nil
}

// YahooTeamUpserter is the interface for database operations needed by Yahoo team import.
type YahooTeamUpserter interface {
	UpsertYahooTeamBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamBatchParams) *sqlcdb.UpsertYahooTeamBatchBatchResults
	UpsertYahooTeamManagerBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamManagerBatchParams) *sqlcdb.UpsertYahooTeamManagerBatchBatchResults
}

func importYahooTeamsImpl(
	ctx context.Context,
	fs store.Store,
	queries YahooTeamUpserter,
	input ImportYahooTeamsInput,
) (ImportYahooTeamsResult, error) {
	result := ImportYahooTeamsResult{}

	if len(input.Teams) == 0 {
		return result, nil
	}

	// Collect all team and manager params
	var teamParams []sqlcdb.UpsertYahooTeamBatchParams
	var managerParams []sqlcdb.UpsertYahooTeamManagerBatchParams

	for _, teamInfo := range input.Teams {
		// Read the team file
		teamFile := store.TeamFile{Season: input.Season, LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID}
		if !fs.Exists(teamFile) {
			log.Debug().
				Int("season", input.Season).
				Int("leagueID", teamInfo.LeagueID).
				Int("teamID", teamInfo.TeamID).
				Msg("No team file found")
			continue
		}

		data, err := fs.Read(teamFile)
		if err != nil {
			return result, fmt.Errorf("read team file %d: %w", teamInfo.TeamID, err)
		}

		fantasy, err := store.ParseXML(data)
		if err != nil {
			return result, fmt.Errorf("parse team XML %d: %w", teamInfo.TeamID, err)
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
			LeagueID:              int32(input.LeagueID),
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
				LeagueID:       int32(input.LeagueID),
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
		results := queries.UpsertYahooTeamBatch(ctx, teamParams)
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
		results := queries.UpsertYahooTeamManagerBatch(ctx, managerParams)
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

	return result, nil
}
