package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
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

	idx, err := buildPathIndex(context.Background(), "../test-data/cache")
	if err != nil {
		b.Fatal(err)
	}
	storage := newIndexedStorage(store.NewDefaultStorage(), idx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := getAllMetrics(context.Background(), redisClient, nil, storage)
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

	loaders := map[core.FileType]assetLoader{
		core.PlayerHeadshot: func(_ context.Context) ([]asset.Asset, error) {
			return assets, nil
		},
	}

	got, err := checkAssetCache(context.Background(), storage, loaders)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, core.PlayerHeadshot, got[0].fileType)
	require.Equal(t, cacheMetricsSeasonAll, got[0].seasonYear)
	require.Equal(t, totalAssets, got[0].expected)
	require.Equal(t, partialFoundCount, got[0].found)
}

// TestCheckAssetCache_LoaderError verifies that a loader failure is propagated
// rather than swallowed (otherwise a Postgres outage would silently report
// expected=0 and look identical to a healthy empty database).
func TestCheckAssetCache_LoaderError(t *testing.T) {
	t.Parallel()

	loaders := map[core.FileType]assetLoader{
		core.PlayerHeadshot: func(_ context.Context) ([]asset.Asset, error) {
			return nil, errors.New("connection refused")
		},
	}

	_, err := checkAssetCache(context.Background(), store.NewMemStorage(), loaders)
	require.Error(t, err)
	require.Contains(t, err.Error(), core.PlayerHeadshot.String())
	require.Contains(t, err.Error(), "connection refused")
}

// TestCheckAssetCache_EmptyLoader verifies that a class with zero rows produces
// expected=0, found=0, and is still emitted (so the gauge resets to zero on a
// freshly-emptied table).
func TestCheckAssetCache_EmptyLoader(t *testing.T) {
	t.Parallel()

	loaders := map[core.FileType]assetLoader{
		core.PlayerHeadshot: func(_ context.Context) ([]asset.Asset, error) {
			return nil, nil
		},
	}

	got, err := checkAssetCache(context.Background(), store.NewMemStorage(), loaders)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 0, got[0].expected)
	require.Equal(t, 0, got[0].found)
}

// TestAppendOnDiskOnlyMetrics_ReconcilesTotals asserts that
// appendOnDiskOnlyMetrics produces a cacheData slice whose rolled-up expected
// and found totals match the on-disk totals from the index — the contract that
// keeps the "Total Files" panel and the "Cache Statistics" panel consistent.
// It exercises three reconciliation cases in one shot:
//   - covered FileType with no surplus (idx_count == sum_expected) → no row added
//   - covered FileType with on-disk surplus (preseason boxscores) → synthetic
//     season=all row carrying the surplus
//   - uncovered FileType (Edge tracking) → synthetic season=all row carrying
//     the full on-disk count
//
// The original per-season scheduled rows must remain untouched so that
// found < expected gaps for missing scheduled files still surface in the
// rolled-up delta.
func TestAppendOnDiskOnlyMetrics_ReconcilesTotals(t *testing.T) {
	t.Parallel()

	const (
		boxscoreExpected = 100
		boxscoreFound    = 80
		boxscoreOnDisk   = 9999
		rosterExpected   = 50
		rosterFound      = 50
		rosterOnDisk     = 50
		edgeOnDisk       = 42
	)
	in := []cacheMetrics{
		{seasonYear: 2024, fileType: core.Boxscore, expected: boxscoreExpected, found: boxscoreFound},
		{seasonYear: 2024, fileType: core.Roster, expected: rosterExpected, found: rosterFound},
	}
	idx := &pathIndex{
		byType: map[core.FileType]*fileTypeStats{
			core.Boxscore:         {count: boxscoreOnDisk},
			core.Roster:           {count: rosterOnDisk},
			core.EdgeSkaterDetail: {count: edgeOnDisk},
		},
	}

	got := appendOnDiskOnlyMetrics(in, idx)

	// Original scheduled rows preserved verbatim.
	require.Equal(t, in[0], got[0], "original covered Boxscore row must not be modified")
	require.Equal(t, in[1], got[1], "original covered Roster row must not be modified")

	// Indexed by (fileType, seasonYear) so we can assert per-row.
	type key struct {
		ft     core.FileType
		season int
	}
	rows := make(map[key]cacheMetrics, len(got))
	for _, m := range got {
		rows[key{m.fileType, m.seasonYear}] = m
	}

	// Boxscore surplus (9999 on disk, 100 scheduled → 9899 surplus).
	surplus, ok := rows[key{core.Boxscore, cacheMetricsSeasonAll}]
	require.True(t, ok, "covered Boxscore with surplus must emit a season=all reconciliation row")
	require.Equal(t, boxscoreOnDisk-boxscoreExpected, surplus.expected)
	require.Equal(t, boxscoreOnDisk-boxscoreExpected, surplus.found)

	// Roster has no surplus (idx == expected) → no extra row.
	_, ok = rows[key{core.Roster, cacheMetricsSeasonAll}]
	require.False(t, ok, "covered Roster with no surplus must not emit a reconciliation row")

	// Uncovered EdgeSkaterDetail backfilled with full on-disk count.
	edge, ok := rows[key{core.EdgeSkaterDetail, cacheMetricsSeasonAll}]
	require.True(t, ok, "uncovered Edge type must be backfilled from index")
	require.Equal(t, edgeOnDisk, edge.expected)
	require.Equal(t, edgeOnDisk, edge.found)

	// Rolled-up totals reconcile exactly with the index.
	var totalExpected, totalFound, idxTotal int
	for _, m := range got {
		totalExpected += m.expected
		totalFound += m.found
	}
	for _, s := range idx.byType {
		idxTotal += int(s.count)
	}
	require.Equal(t, idxTotal, totalExpected, "expected total must equal on-disk total after reconciliation")
	require.Equal(t, idxTotal-(boxscoreExpected-boxscoreFound), totalFound, "found total must equal on-disk total minus genuinely missing scheduled files")
}

// TestAppendOnDiskOnlyMetrics_SkipsZeroCountTypes asserts that FileTypes with
// no on-disk presence and no schedule coverage (the common case for unused
// types) do not produce noise rows. Without this, every never-used FileType
// would emit an expected=0/found=0 gauge for season=all.
func TestAppendOnDiskOnlyMetrics_SkipsZeroCountTypes(t *testing.T) {
	t.Parallel()

	idx := &pathIndex{
		byType: map[core.FileType]*fileTypeStats{
			core.Boxscore: {count: 5},
		},
	}
	got := appendOnDiskOnlyMetrics(nil, idx)

	require.Len(t, got, 1, "only the on-disk Boxscore should yield a row; absent types must not emit zero-count noise")
	require.Equal(t, core.Boxscore, got[0].fileType)
	require.Equal(t, 5, got[0].expected)
	require.Equal(t, 5, got[0].found)
}

// TestCacheMetricsSeasonLabel verifies the sentinel renders as "all" for
// season-independent metrics and as a year string otherwise.
func TestCacheMetricsSeasonLabel(t *testing.T) {
	t.Parallel()

	require.Equal(t, "all", cacheMetrics{seasonYear: cacheMetricsSeasonAll}.seasonLabel())
	require.Equal(t, "2024", cacheMetrics{seasonYear: 2024}.seasonLabel())
}

// TestNewAssetCacheLoaders verifies the production builder returns one entry
// per file type the asset workflow handles. A drift between this list and the
// nine registered loaders would silently drop a class from the metrics.
//
// nil queries works here because asset.Activities does not dereference its
// Queries field at construction; this test asserts the map shape only and
// never invokes a loader. If asset.Activities ever validates Queries on
// construction, this call must switch to a real (or empty) sqlc.Queries.
func TestNewAssetCacheLoaders(t *testing.T) {
	t.Parallel()

	loaders := newAssetCacheLoaders(nil)
	require.Len(t, loaders, 7)
	want := map[core.FileType]bool{
		core.PlayerHeadshot:    true,
		core.PlayerHeroImage:   true,
		core.PlayerYahooImage:  true,
		core.TeamLogo:          true,
		core.YahooTeamLogo:     true,
		core.YahooLeagueLogo:   true,
		core.YahooManagerImage: true,
	}
	for ft := range loaders {
		require.True(t, want[ft], "unexpected fileType %q", ft)
		delete(want, ft)
	}
	require.Empty(t, want, "missing classes: %v", want)
}
