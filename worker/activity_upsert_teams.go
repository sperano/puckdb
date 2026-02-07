package worker

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

const (
	HistoricalConferenceID = 3
	HistoricalDivisionID   = 5
)

// UpsertMissingTeamsResult contains the results of upserting missing teams.
type UpsertMissingTeamsResult struct {
	TeamsInserted int `json:"teamsInserted"`
	TeamsSkipped  int `json:"teamsSkipped"`
}

// UpsertMissingTeamsActivity inserts any teams that don't already exist in the database.
// Teams are assigned to the "Historical" division since they are defunct franchises.
func UpsertMissingTeamsActivity(ctx context.Context, teams []ExtractedTeam) (UpsertMissingTeamsResult, error) {
	logger := activity.GetLogger(ctx)

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return UpsertMissingTeamsResult{}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	return upsertMissingTeamsImpl(ctx, queries, teams, logger)
}

type teamUpserter interface {
	GetAllNHLTeamIDs(ctx context.Context) ([]int64, error)
	UpsertNHLConference(ctx context.Context, arg sqlcdb.UpsertNHLConferenceParams) error
	UpsertNHLDivision(ctx context.Context, arg sqlcdb.UpsertNHLDivisionParams) error
	UpsertNHLTeam(ctx context.Context, arg sqlcdb.UpsertNHLTeamParams) error
}

func upsertMissingTeamsImpl(ctx context.Context, queries teamUpserter, teams []ExtractedTeam, logger activityLogger) (UpsertMissingTeamsResult, error) {
	result := UpsertMissingTeamsResult{}

	// Get existing team IDs
	existingIDs, err := queries.GetAllNHLTeamIDs(ctx)
	if err != nil {
		return result, fmt.Errorf("get existing team IDs: %w", err)
	}

	existingSet := make(map[int64]struct{}, len(existingIDs))
	for _, id := range existingIDs {
		existingSet[id] = struct{}{}
	}

	// Find teams that need to be inserted
	var missingTeams []ExtractedTeam
	for _, team := range teams {
		if _, exists := existingSet[team.ID]; exists {
			result.TeamsSkipped++
		} else {
			missingTeams = append(missingTeams, team)
		}
	}

	if len(missingTeams) == 0 {
		logger.Info("No missing teams to insert", "extracted", len(teams), "skipped", result.TeamsSkipped)
		return result, nil
	}

	// Ensure Historical conference and division exist
	if err := ensureHistoricalDivision(ctx, queries); err != nil {
		return result, fmt.Errorf("ensure historical division: %w", err)
	}

	// Insert missing teams
	for _, team := range missingTeams {
		params := sqlcdb.UpsertNHLTeamParams{
			ID:            team.ID,
			YahooID:       pgtype.Int8{Valid: false},
			City:          team.City,
			Name:          team.Name,
			Abbreviation:  team.Abbreviation,
			NHLDivisionID: HistoricalDivisionID,
			NHLHomeLink:   "",
			YahooHomeLink: "",
			SmallLogoURL:  "",
			LargeLogoURL:  "",
			AllStars:      false,
		}

		if err := queries.UpsertNHLTeam(ctx, params); err != nil {
			return result, fmt.Errorf("upsert team %d (%s): %w", team.ID, team.Abbreviation, err)
		}

		logger.Info("Inserted historical team",
			"id", team.ID,
			"abbreviation", team.Abbreviation,
			"city", team.City,
			"name", team.Name)

		result.TeamsInserted++
	}

	log.Info().
		Int("inserted", result.TeamsInserted).
		Int("skipped", result.TeamsSkipped).
		Msg("Team upsert complete")

	return result, nil
}

func ensureHistoricalDivision(ctx context.Context, queries teamUpserter) error {
	// Ensure Historical conference exists
	if err := queries.UpsertNHLConference(ctx, sqlcdb.UpsertNHLConferenceParams{
		ID:   HistoricalConferenceID,
		Name: "Historical",
	}); err != nil {
		return fmt.Errorf("upsert historical conference: %w", err)
	}

	// Ensure Historical division exists
	if err := queries.UpsertNHLDivision(ctx, sqlcdb.UpsertNHLDivisionParams{
		ID:              HistoricalDivisionID,
		Name:            "Historical",
		NHLConferenceID: HistoricalConferenceID,
	}); err != nil {
		return fmt.Errorf("upsert historical division: %w", err)
	}

	return nil
}
