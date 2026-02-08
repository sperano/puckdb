package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// UpsertFranchisesResult contains the results of upserting franchises.
type UpsertFranchisesResult struct {
	FranchisesUpserted int `json:"franchisesUpserted"`
}

// UpsertFranchisesActivity reads franchises from the cache and upserts them to the database.
func UpsertFranchisesActivity(ctx context.Context) (UpsertFranchisesResult, error) {
	logger := activity.GetLogger(ctx)

	// Read franchises from cache
	fs := cache.NewSimpleCache()
	file := cache.FranchisesFile{}

	data, err := fs.Read(file)
	if err != nil {
		return UpsertFranchisesResult{}, fmt.Errorf("read franchises from cache: %w", err)
	}

	var franchises []nhl.Franchise
	if err := json.Unmarshal(data, &franchises); err != nil {
		return UpsertFranchisesResult{}, fmt.Errorf("unmarshal franchises: %w", err)
	}

	// Open database connection
	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return UpsertFranchisesResult{}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	return upsertFranchisesImpl(ctx, queries, franchises, logger)
}

type franchiseUpserter interface {
	UpsertNHLFranchise(ctx context.Context, arg sqlcdb.UpsertNHLFranchiseParams) error
}

func upsertFranchisesImpl(
	ctx context.Context,
	queries franchiseUpserter,
	franchises []nhl.Franchise,
	logger activityLogger,
) (UpsertFranchisesResult, error) {
	result := UpsertFranchisesResult{}

	for _, f := range franchises {
		params := sqlcdb.UpsertNHLFranchiseParams{
			ID:             f.ID,
			FullName:       f.FullName,
			TeamCommonName: f.TeamCommonName,
			TeamPlaceName:  f.TeamPlaceName,
		}

		if err := queries.UpsertNHLFranchise(ctx, params); err != nil {
			return result, fmt.Errorf("upsert franchise %d (%s): %w", f.ID, f.FullName, err)
		}

		result.FranchisesUpserted++
	}

	logger.Info("Franchises upserted", "count", result.FranchisesUpserted)
	log.Info().Int("count", result.FranchisesUpserted).Msg("Franchises upserted to database")

	return result, nil
}
