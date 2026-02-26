package worker

import (
	"context"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// UpsertFranchisesResult contains the results of upserting franchises.
type UpsertFranchisesResult struct {
	FranchisesUpserted int `json:"franchisesUpserted"`
}

// UpsertFranchisesActivity reads franchises from the cache and upserts them to the database.
func UpsertFranchisesActivity(ctx context.Context) (UpsertFranchisesResult, error) {
	logger := activity.GetLogger(ctx)

	repos := store.NewDefaultRepos()

	franchises, err := repos.Franchise.Get()
	if err != nil {
		return UpsertFranchisesResult{}, fmt.Errorf("read franchises from cache: %w", err)
	}

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return UpsertFranchisesResult{}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	result, err := upsertFranchisesImpl(ctx, queries, franchises)
	if err != nil {
		return result, err
	}

	logger.Info("Franchises upserted", "count", result.FranchisesUpserted)
	return result, nil
}

type franchiseUpserter interface {
	UpsertFranchise(ctx context.Context, arg sqlcdb.UpsertFranchiseParams) error
}

func upsertFranchisesImpl(
	ctx context.Context,
	queries franchiseUpserter,
	franchises []nhl.Franchise,
) (UpsertFranchisesResult, error) {
	result := UpsertFranchisesResult{}

	for _, f := range franchises {
		params := sqlcdb.UpsertFranchiseParams{
			ID:             f.ID,
			FullName:       f.FullName,
			TeamCommonName: f.TeamCommonName,
			TeamPlaceName:  f.TeamPlaceName,
		}

		if err := queries.UpsertFranchise(ctx, params); err != nil {
			return result, fmt.Errorf("upsert franchise %d (%s): %w", f.ID, f.FullName, err)
		}

		result.FranchisesUpserted++
	}

	return result, nil
}
