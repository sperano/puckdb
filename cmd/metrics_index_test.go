package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/metricsfixtures"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/require"
)

// writeFixture writes a single fixture file with len(content)==size bytes so
// per-type byte totals are deterministic.
func writeFixture(t *testing.T, root, rel string, size int) {
	t.Helper()
	full := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, make([]byte, size), 0o644))
}

// TestBuildPathIndex_FullTreeAndStats walks a synthetic data path that covers
// several top-level subtrees in parallel and verifies every file is indexed
// with its size and classification.
func TestBuildPathIndex_FullTreeAndStats(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// One file per relevant subtree, sizes chosen so any per-type accounting
	// bug shows up as a wrong byte total instead of an accidental match.
	writeFixture(t, root, "seasons/2024/games/2024/01/15/boxscore-2024020001.json", 11)
	writeFixture(t, root, "seasons/2024/games/2024/01/15/playbyplay-2024020001.json", 13)
	writeFixture(t, root, "seasons/2024/games/2024/01/15/daily-schedule-2024-01-15.json", 17)
	writeFixture(t, root, "seasons/2024/rosters/roster-NYR.json", 19)
	writeFixture(t, root, "assets/players/8478402/headshot.png", 23)
	writeFixture(t, root, "edge/skaters/8478402/detail-20242025-2.json", 29)
	writeFixture(t, root, "nhl/franchises.json", 31)
	writeFixture(t, root, ".git/HEAD", 37) // must be skipped

	idx, err := buildPathIndex(context.Background(), root)
	require.NoError(t, err)

	// Presence map covers everything except the .git contents.
	require.True(t, idx.has("seasons/2024/games/2024/01/15/boxscore-2024020001.json"))
	require.True(t, idx.has("seasons/2024/games/2024/01/15/playbyplay-2024020001.json"))
	require.True(t, idx.has("seasons/2024/games/2024/01/15/daily-schedule-2024-01-15.json"))
	require.True(t, idx.has("seasons/2024/rosters/roster-NYR.json"))
	require.True(t, idx.has("assets/players/8478402/headshot.png"))
	require.True(t, idx.has("edge/skaters/8478402/detail-20242025-2.json"))
	require.True(t, idx.has("nhl/franchises.json"))
	require.False(t, idx.has(".git/HEAD"))
	require.False(t, idx.has("seasons/2024/games/2024/01/15/boxscore-9999999999.json"))

	// Total bytes excludes the .git file.
	const wantTotal = 11 + 13 + 17 + 19 + 23 + 29 + 31
	require.Equal(t, int64(wantTotal), idx.totalBytes)

	// Per-type counts.
	require.Equal(t, int64(1), idx.byType[core.Boxscore].count)
	require.Equal(t, int64(11), idx.byType[core.Boxscore].bytes)
	require.Equal(t, int64(1), idx.byType[core.PlayByPlay].count)
	require.Equal(t, int64(1), idx.byType[core.DailySchedule].count)
	require.Equal(t, int64(1), idx.byType[core.SeasonRoster].count)
	require.Equal(t, int64(1), idx.byType[core.PlayerHeadshot].count)
	require.Equal(t, int64(1), idx.byType[core.EdgeSkaterDetail].count)
	require.Equal(t, int64(1), idx.byType[core.Franchises].count)
}

// TestBuildPathIndex_EmptyDataPath returns a usable empty index rather than
// erroring, so callers running without a data-path don't have to special-case.
func TestBuildPathIndex_EmptyDataPath(t *testing.T) {
	t.Parallel()

	idx, err := buildPathIndex(context.Background(), "")
	require.NoError(t, err)
	require.NotNil(t, idx)
	require.Empty(t, idx.present)
	require.Equal(t, int64(0), idx.totalBytes)
	require.False(t, idx.has("anything"))
}

// TestBuildPathIndex_MissingDataPath surfaces the read error to the caller —
// silently returning an empty index would mask a misconfiguration as a
// completely empty cache.
func TestBuildPathIndex_MissingDataPath(t *testing.T) {
	t.Parallel()

	_, err := buildPathIndex(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
}

// TestIndexedStorage_Exists confirms the adapter's only behavioural change:
// Exists answers from the index and never touches the wrapped Storage's disk.
// Read/Write/Delete still pass through.
func TestIndexedStorage_Exists(t *testing.T) {
	t.Parallel()

	inner := store.NewMemStorage()
	idx := &pathIndex{
		present: map[string]struct{}{
			"seasons/2024/games/2024/01/15/boxscore-1.json": {},
		},
	}
	s := newIndexedStorage(inner, idx)

	ctx := context.Background()
	require.True(t, s.Exists(ctx, "seasons/2024/games/2024/01/15/boxscore-1.json"))
	require.False(t, s.Exists(ctx, "seasons/2024/games/2024/01/15/boxscore-2.json"))

	// Writes still go through to the inner storage; this must NOT cause
	// the index to start reporting the new path as present, because the
	// index is a snapshot from earlier in the tick.
	require.NoError(t, s.Write(ctx, "new.json", []byte("data")))
	require.False(t, s.Exists(ctx, "new.json"))

	data, err := s.Read(ctx, "new.json")
	require.NoError(t, err)
	require.Equal(t, []byte("data"), data)
}

// A file type given to the indexed storage reaches the instrumented storage
// it wraps, so pass-through reads keep their metrics label. Not parallel: it
// reads the process-wide metrics registry.
func TestIndexedStorage_WithFileTypeLabelsWrappedStorage(t *testing.T) {
	inner := store.NewMemStorage()
	const path = "seasons/2024/games/2025/01/15/boxscore-3.json"
	inner.SetFile(path, []byte("data"))
	s := newIndexedStorage(store.NewInstrumentedStorage(inner), &pathIndex{present: map[string]struct{}{path: {}}})
	readBefore := metricsfixtures.FSOpCount(t, "read", core.Boxscore)
	existsBefore := metricsfixtures.FSOpCount(t, "exists", core.Boxscore)

	labeled := store.WithFileType(s, core.Boxscore)
	_, err := labeled.Read(context.Background(), path)
	require.NoError(t, err)
	require.True(t, labeled.Exists(context.Background(), path))

	require.Equal(t, readBefore+1, metricsfixtures.FSOpCount(t, "read", core.Boxscore))
	require.Equal(t, existsBefore, metricsfixtures.FSOpCount(t, "exists", core.Boxscore),
		"Exists must still be answered by the index, not the wrapped storage")
}

// TestPathIndex_HasPlatformSeparators ensures lookups work even when callers
// build paths with filepath.Join (which uses OS-native separators on Windows).
// Forward-slash normalisation is what makes that round-trip safe.
func TestPathIndex_HasPlatformSeparators(t *testing.T) {
	t.Parallel()

	idx := &pathIndex{
		present: map[string]struct{}{"a/b/c.json": {}},
	}
	require.True(t, idx.has("a/b/c.json"))
	require.True(t, idx.has(filepath.Join("a", "b", "c.json")))
	require.False(t, idx.has("a/b/missing.json"))
}

// TestBuildPathIndex_SkipsTempFiles guards the cache gauges against temp
// files orphaned by a crash mid-write: they must contribute neither presence
// nor bytes, on both the fan-out and the WalkDir scan paths.
func TestBuildPathIndex_SkipsTempFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const realSize = 11
	writeFixture(t, root, "seasons/2024/games/2024/01/15/boxscore-2024020001.json", realSize)

	// One orphan deep in the seasons tree (WalkDir path) and one at the top
	// level (fan-out ReadDir path), both named exactly as FSStorage.Write
	// stages them.
	deepDir := filepath.Join(root, "seasons/2024/games/2024/01/15")
	for _, dir := range []string{deepDir, root} {
		orphan := filepath.Join(dir, ".boxscore-2024020002.json.tmp-1a2b3c4d5e6f7a8b")
		require.True(t, store.IsTempFile(filepath.Base(orphan)))
		require.NoError(t, os.WriteFile(orphan, make([]byte, 1000), 0o644))
	}

	idx, err := buildPathIndex(context.Background(), root)
	require.NoError(t, err)

	require.True(t, idx.has("seasons/2024/games/2024/01/15/boxscore-2024020001.json"))
	require.False(t, idx.has("seasons/2024/games/2024/01/15/.boxscore-2024020002.json.tmp-1a2b3c4d5e6f7a8b"))
	require.False(t, idx.has(".boxscore-2024020002.json.tmp-1a2b3c4d5e6f7a8b"))
	require.Equal(t, int64(realSize), idx.totalBytes)
	require.Equal(t, int64(1), idx.byType[core.Boxscore].count)
}
