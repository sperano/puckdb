package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
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
	NHLClient NHLClient
}

// FetchFranchisesResult contains statistics from fetching franchises.
type FetchFranchisesResult struct {
	Origin core.DataOrigin // Where the data came from
}

// FetchFranchises fetches all NHL franchises from the API.
// Uses Redis → FileSystem cache; skips fetch if already cached.
func (a *FranchiseActivities) FetchFranchises(ctx context.Context) (FetchFranchisesResult, error) {
	franchisesRes := resource.Franchises{}
	logger := activity.GetLogger(ctx)

	// Check Redis → FileSystem cache
	cached, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, franchisesRes)
	if err == nil {
		logger.Debug("Franchises loaded from cache", "count", len(cached.Data), "origin", origin.String())
		metrics.IncDownload(core.Franchises, metrics.ResultHit)
		return FetchFranchisesResult{Origin: origin}, nil
	}
	// Cache miss - fetch from API
	start := time.Now()
	franchises, err := a.NHLClient.Franchises(ctx)
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
		metrics.IncDownload(core.Franchises, metrics.ResultError)
		return FetchFranchisesResult{}, err
	}
	// Save to FileSystem cache in wrapped format (matches API response structure)
	response := nhl.FranchisesResponse{Data: franchises}
	data, _ := json.Marshal(response)
	if err := a.Storage.Write(franchisesRes.Path(), data); err != nil {
		return FetchFranchisesResult{}, fmt.Errorf("failed to write franchises to cache: %w", err)
	}
	// Populate Redis gob cache
	if err := cache.Set(a.GobCache, ctx, core.RedisKey(franchisesRes), response); err != nil {
		return FetchFranchisesResult{}, fmt.Errorf("gob cache set franchises: %w", err)
	}

	logger.Info("Franchises fetched from API", "count", len(franchises))
	metrics.IncDownload(core.Franchises, metrics.ResultMiss)
	return FetchFranchisesResult{Origin: core.OriginRemoteNHLAPI}, nil
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
