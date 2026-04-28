package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/asset"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func BenchmarkGetAllMetrics(b *testing.B) {
	viper.Set(config.FlagDataPath, "../test-data/cache")
	viper.Set(config.FlagSeasonYear, 2022)

	redisClient := cache.NewClient()
	defer redisClient.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := getAllMetrics(context.Background(), redisClient, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// makeHeadshotAssets builds n PlayerHeadshot assets with sequential player IDs,
// using a .png extension so asset.Path() succeeds.
func makeHeadshotAssets(n int) []asset.Asset {
	out := make([]asset.Asset, n)
	for i := range out {
		out[i] = asset.NewPlayerHeadshot(
			nhl.PlayerID(8478000+i),
			"https://assets.nhle.com/mugs/test.png",
		)
	}
	return out
}

// TestCheckAssetCache_FullAndPartial verifies that found counts existing files
// while expected counts every row the loader returned, including missing ones.
func TestCheckAssetCache_FullAndPartial(t *testing.T) {
	t.Parallel()

	const (
		totalAssets       = 10
		partialFoundCount = 6
	)
	assets := makeHeadshotAssets(totalAssets)
	storage := store.NewMemStorage()
	// Pre-populate part of the expected paths to simulate a partial cache.
	for i := range partialFoundCount {
		p, err := assets[i].Path()
		require.NoError(t, err)
		require.NoError(t, storage.Write(context.Background(), p, []byte("x")))
	}

	classes := []assetCacheClass{
		{store.FileTypePlayerHeadshot, func(_ context.Context) ([]asset.Asset, error) {
			return assets, nil
		}},
	}

	got, err := checkAssetCache(context.Background(), storage, classes)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, store.FileTypePlayerHeadshot, got[0].fileType)
	require.Equal(t, cacheMetricsSeasonAll, got[0].seasonYear)
	require.Equal(t, totalAssets, got[0].expected)
	require.Equal(t, partialFoundCount, got[0].found)
}

// TestCheckAssetCache_LoaderError verifies that a loader failure is propagated
// rather than swallowed (otherwise a Postgres outage would silently report
// expected=0 and look identical to a healthy empty database).
func TestCheckAssetCache_LoaderError(t *testing.T) {
	t.Parallel()

	classes := []assetCacheClass{
		{store.FileTypePlayerHeadshot, func(_ context.Context) ([]asset.Asset, error) {
			return nil, errors.New("connection refused")
		}},
	}

	_, err := checkAssetCache(context.Background(), store.NewMemStorage(), classes)
	require.Error(t, err)
	require.Contains(t, err.Error(), store.FileTypePlayerHeadshot)
	require.Contains(t, err.Error(), "connection refused")
}

// TestCheckAssetCache_EmptyLoader verifies that a class with zero rows produces
// expected=0, found=0, and is still emitted (so the gauge resets to zero on a
// freshly-emptied table).
func TestCheckAssetCache_EmptyLoader(t *testing.T) {
	t.Parallel()

	classes := []assetCacheClass{
		{store.FileTypePlayerHeadshot, func(_ context.Context) ([]asset.Asset, error) {
			return nil, nil
		}},
	}

	got, err := checkAssetCache(context.Background(), store.NewMemStorage(), classes)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 0, got[0].expected)
	require.Equal(t, 0, got[0].found)
}

// TestCacheMetricsSeasonLabel verifies the sentinel renders as "all" for
// season-independent metrics and as a year string otherwise.
func TestCacheMetricsSeasonLabel(t *testing.T) {
	t.Parallel()

	require.Equal(t, "all", cacheMetrics{seasonYear: cacheMetricsSeasonAll}.seasonLabel())
	require.Equal(t, "2024", cacheMetrics{seasonYear: 2024}.seasonLabel())
}

// TestNewAssetCacheClasses verifies the production builder returns one entry
// per file type the asset workflow handles. A drift between this list and the
// nine registered loaders would silently drop a class from the metrics.
//
// nil queries works here because asset.Activities does not dereference its
// Queries field at construction; this test asserts the list shape only and
// never invokes a loader. If asset.Activities ever validates Queries on
// construction, this call must switch to a real (or empty) sqlc.Queries.
func TestNewAssetCacheClasses(t *testing.T) {
	t.Parallel()

	classes := newAssetCacheClasses(nil)
	require.Len(t, classes, 9)
	want := map[string]bool{
		store.FileTypePlayerHeadshot:         true,
		store.FileTypePlayerHeroImage:        true,
		store.FileTypePlayerYahooImageSmall:  true,
		store.FileTypePlayerYahooImageMedium: true,
		store.FileTypePlayerYahooImageLarge:  true,
		store.FileTypeTeamLogo:               true,
		store.FileTypeYahooTeamLogo:          true,
		store.FileTypeYahooLeagueLogo:        true,
		store.FileTypeYahooManagerImage:      true,
	}
	for _, c := range classes {
		require.True(t, want[c.fileType], "unexpected fileType %q", c.fileType)
		delete(want, c.fileType)
	}
	require.Empty(t, want, "missing classes: %v", want)
}
