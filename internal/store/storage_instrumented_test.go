package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/metricsfixtures"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstrumentedStorage_Read(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	// Setup
	path := "seasons/2023/games/2024/01/15/daily-schedule-2024-01-15.json"
	inner.SetFile(path, []byte(`{"games": []}`))

	// Test
	data, err := storage.Read(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"games": []}`), data)
}

func TestInstrumentedStorage_Write(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	path := "seasons/2023/games/2024/01/15/boxscore-2024020123.json"
	err := storage.Write(context.Background(), path, []byte(`{"id": 2024020123}`))
	require.NoError(t, err)

	// Verify via inner storage
	assert.True(t, inner.Has(path))
}

func TestInstrumentedStorage_Exists(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	path := "player-landings/player-8474564.json"
	assert.False(t, storage.Exists(context.Background(), path))

	inner.SetFile(path, []byte(`{}`))
	assert.True(t, storage.Exists(context.Background(), path))
}

func TestInstrumentedStorage_Delete(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	path := "player-landings/missing/player-12345.json"
	inner.SetFile(path, []byte(`{}`))

	err := storage.Delete(context.Background(), path)
	require.NoError(t, err)
	assert.False(t, inner.Has(path))
}

func TestInstrumentedStorage_List(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	// Setup
	inner.SetFile("seasons/2024/games/2025/01/15/boxscore-2024020100.json", []byte(`{}`))
	inner.SetFile("seasons/2024/games/2025/01/15/boxscore-2024020101.json", []byte(`{}`))

	files, err := storage.List(context.Background(), "seasons/2024/games/2025/01/15", "json")
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

// failingWriteStorage is a MemStorage whose writes fail with err.
type failingWriteStorage struct {
	*MemStorage
	err error
}

func (s failingWriteStorage) Write(context.Context, string, []byte) error { return s.err }

// fsRecord is one operation seen by recordingStorage's observer.
type fsRecord struct {
	operation string
	fileType  core.FileType
	bytes     int
}

// fsRecorder collects observed operations; labeled views share it.
type fsRecorder struct {
	mu      sync.Mutex
	records []fsRecord
}

func (r *fsRecorder) observe(operation string, ft core.FileType, _ time.Duration, bytes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, fsRecord{operation: operation, fileType: ft, bytes: bytes})
}

func (r *fsRecorder) all() []fsRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fsRecord(nil), r.records...)
}

// recordingStorage returns an InstrumentedStorage over inner that records to
// a fsRecorder and logs warnings to a buffer instead of the global metrics
// and logger, so tests can run in parallel.
func recordingStorage(inner Storage) (*InstrumentedStorage, *fsRecorder, *bytes.Buffer) {
	rec := &fsRecorder{}
	var logs bytes.Buffer
	s := NewInstrumentedStorage(inner)
	s.observe = rec.observe
	s.unlabeled = &unlabeledWarner{logger: zerolog.New(&logs)}
	return s, rec, &logs
}

func TestInstrumentedStorage_WithFileTypeLabelsEveryOperation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := NewMemStorage()
	s, rec, logs := recordingStorage(inner)
	labeled := s.WithFileType(core.Boxscore)

	path := "seasons/2024/games/2025/01/15/boxscore-2024020123.json"
	data := []byte(`{"id":2024020123}`)
	require.NoError(t, labeled.Write(ctx, path, data))
	assert.True(t, labeled.Exists(ctx, path))
	_, err := labeled.Stat(ctx, path)
	require.NoError(t, err)
	_, err = labeled.Read(ctx, path)
	require.NoError(t, err)
	files, err := labeled.List(ctx, "seasons/2024/games/2025/01/15", "json")
	require.NoError(t, err)
	require.NoError(t, labeled.Delete(ctx, path))

	assert.Equal(t, []fsRecord{
		{opWrite, core.Boxscore, len(data)},
		{opExists, core.Boxscore, 0},
		{opStat, core.Boxscore, 0},
		{opRead, core.Boxscore, len(data)},
		{opList, core.Boxscore, len(files)},
		{opDelete, core.Boxscore, 0},
	}, rec.all())
	assert.Empty(t, logs.String(), "labeled operations must not warn")
}

func TestInstrumentedStorage_FailedReadRecordsNoBytes(t *testing.T) {
	t.Parallel()
	s, rec, _ := recordingStorage(NewMemStorage())

	_, err := s.WithFileType(core.PlayByPlay).Read(context.Background(), "missing.json")
	require.ErrorIs(t, err, os.ErrNotExist)

	assert.Equal(t, []fsRecord{{opRead, core.PlayByPlay, 0}}, rec.all())
}

func TestInstrumentedStorage_FailedWriteRecordsNoBytes(t *testing.T) {
	t.Parallel()
	s, rec, _ := recordingStorage(failingWriteStorage{MemStorage: NewMemStorage(), err: errors.New("disk full")})

	err := s.WithFileType(core.ShiftChart).Write(context.Background(), "shiftchart.json", []byte("data"))
	require.Error(t, err)

	assert.Equal(t, []fsRecord{{opWrite, core.ShiftChart, 0}}, rec.all())
}

// An operation without a file type is still recorded, under Unknown, and
// the first one of each kind is logged with its path so the caller can be
// found.
func TestInstrumentedStorage_UnlabeledOperationsAreUnknownAndWarnedOncePerKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, rec, logs := recordingStorage(NewMemStorage())
	labeled := s.WithFileType(core.GameStory)

	s.Exists(ctx, "first.json")
	labeled.Exists(ctx, "labeled.json")
	s.Exists(ctx, "second.json")
	_, _ = s.Read(ctx, "third.json")

	assert.Equal(t, []fsRecord{
		{opExists, core.Unknown, 0},
		{opExists, core.GameStory, 0},
		{opExists, core.Unknown, 0},
		{opRead, core.Unknown, 0},
	}, rec.all())

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	require.Len(t, lines, 2, "one warning per operation kind: %s", logs.String())
	assert.Contains(t, lines[0], `"operation":"exists"`)
	assert.Contains(t, lines[0], `"path":"first.json"`)
	assert.Contains(t, lines[0], `"file_type":"Unknown"`)
	assert.Contains(t, lines[1], `"operation":"read"`)
	assert.Contains(t, lines[1], `"path":"third.json"`)
}

func TestInstrumentedStorage_WithFileTypeLeavesParentUnlabeled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, rec, _ := recordingStorage(NewMemStorage())

	s.WithFileType(core.League).Exists(ctx, "league.xml")
	s.Exists(ctx, "league.xml")

	assert.Equal(t, []fsRecord{
		{opExists, core.League, 0},
		{opExists, core.Unknown, 0},
	}, rec.all())
}

// The production default records to the worker metrics registry under the
// labeled file type. Not parallel: it reads the process-wide registry.
func TestNewInstrumentedStorage_RecordsWorkerMetrics(t *testing.T) {
	s := NewInstrumentedStorage(NewMemStorage())
	before := metricsfixtures.FSOpCount(t, opExists, core.YahooManagerImage)

	s.WithFileType(core.YahooManagerImage).Exists(context.Background(), "manager.png")

	assert.Equal(t, before+1, metricsfixtures.FSOpCount(t, opExists, core.YahooManagerImage))
}

func TestWithFileType_StorageWithoutLabelsIsUnchanged(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()

	assert.Same(t, inner, WithFileType(inner, core.Roster))
}

// Labeling a wrapper labels the instrumented storage it wraps.
func TestWithFileType_ReachesNestedInstrumentedStorage(t *testing.T) {
	t.Parallel()
	inner, rec, _ := recordingStorage(NewMemStorage())
	outer, _, _ := recordingStorage(inner)

	WithFileType(outer, core.TeamSummary).Exists(context.Background(), "summary.xml")

	assert.Equal(t, []fsRecord{{opExists, core.TeamSummary, 0}}, rec.all())
}
