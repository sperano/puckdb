package nhl

import (
	"context"
	"fmt"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// franchiseUpserter defines the interface for upserting franchises.
type franchiseUpserter interface {
	UpsertFranchise(ctx context.Context, arg sqlcdb.UpsertFranchiseParams) error
}

// FranchiseActivities holds dependencies for franchise-related activities.
type FranchiseActivities struct {
	Storage   store.Storage
	GobCache  *cache.GobCache
	Upserter  franchiseUpserter
	NHLClient shared.NHLClient
}

// FetchFranchisesResult contains statistics from fetching franchises.
type FetchFranchisesResult struct {
	Origin core.DataOrigin
}

// FetchFranchises fetches all NHL franchises from the API.
// Uses Redis → FileSystem cache; skips fetch if already cached.
func (a *FranchiseActivities) FetchFranchises(ctx context.Context) (FetchFranchisesResult, error) {
	logger := activity.GetLogger(ctx)

	response, origin, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, resource.Franchises{},
		func(ctx context.Context) (nhlapi.FranchisesResponse, error) {
			franchises, err := a.NHLClient.Franchises(ctx)
			if err != nil {
				return nhlapi.FranchisesResponse{}, err
			}
			return nhlapi.FranchisesResponse{Data: franchises}, nil
		},
	)
	if err != nil {
		return FetchFranchisesResult{}, err
	}

	if origin == core.OriginRemoteNHLAPI {
		logger.Info("Franchises fetched from API", "count", len(response.Data))
	} else {
		logger.Debug("Franchises loaded from cache", "count", len(response.Data), "origin", origin.String())
	}
	return FetchFranchisesResult{Origin: origin}, nil
}

// UpsertFranchisesResult contains the results of upserting franchises.
type UpsertFranchisesResult struct {
	FranchisesOrigin   core.DataOrigin
	FranchisesUpserted int `json:"franchisesUpserted"`
}

// UpsertFranchises reads franchises from the cache and upserts them to the database.
func (a *FranchiseActivities) UpsertFranchises(ctx context.Context) (UpsertFranchisesResult, error) {
	logger := activity.GetLogger(ctx)

	franchises, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, resource.Franchises{})
	if err != nil {
		return UpsertFranchisesResult{}, fmt.Errorf("read franchises from %s: %w", origin, err)
	}

	logger.Debug("Read franchises", "count", len(franchises.Data), "origin", origin)

	result := UpsertFranchisesResult{FranchisesOrigin: origin}
	for _, f := range franchises.Data {
		params := sqlcdb.UpsertFranchiseParams{
			ID:             f.ID,
			FullName:       f.FullName,
			TeamCommonName: f.TeamCommonName,
			TeamPlaceName:  f.TeamPlaceName,
		}

		if err := a.Upserter.UpsertFranchise(ctx, params); err != nil {
			return result, fmt.Errorf("upsert franchise %d (%s): %w", f.ID, f.FullName, err)
		}
		result.FranchisesUpserted++
	}

	logger.Info("Franchises upserted", "count", result.FranchisesUpserted)
	return result, nil
}
