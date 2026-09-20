package cache

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newProgressRedis returns a real (in-process) Redis and a client bound to
// it. miniredis rather than redismock because DeleteStaleProgressReport is
// a Lua script: what matters is the resulting key state, not the command
// that was sent.
func newProgressRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return srv, client
}

func TestSaveProgressReport_RoundTripWithRunStampAndTTL(t *testing.T) {
	t.Parallel()
	srv, client := newProgressRedis(t)
	ctx := context.Background()

	data := []byte(`{"status":"running"}`)
	require.NoError(t, SaveProgressReport(ctx, client, "wf-save", "run-1", data))

	got, err := LoadProgressReport(ctx, client, "wf-save")
	require.NoError(t, err)
	assert.Equal(t, data, got)

	key := progressReportKey("wf-save")
	assert.Equal(t, "run-1", srv.HGet(key, progressFieldRun))
	assert.Equal(t, ProgressTTL, srv.TTL(key), "report must carry its expiry")
}

func TestSaveProgressReport_OverwritesPreviousRun(t *testing.T) {
	t.Parallel()
	srv, client := newProgressRedis(t)
	ctx := context.Background()

	require.NoError(t, SaveProgressReport(ctx, client, "wf", "run-old", []byte("old")))
	require.NoError(t, SaveProgressReport(ctx, client, "wf", "run-new", []byte("new")))

	got, err := LoadProgressReport(ctx, client, "wf")
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), got)
	assert.Equal(t, "run-new", srv.HGet(progressReportKey("wf"), progressFieldRun))
}

func TestSaveProgressReport_Error(t *testing.T) {
	t.Parallel()
	srv, client := newProgressRedis(t)
	srv.SetError("connection refused")

	err := SaveProgressReport(context.Background(), client, "wf-err", "run-1", []byte(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "save progress report")
}

func TestLoadProgressReport_Miss(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)

	got, err := LoadProgressReport(context.Background(), client, "wf-missing")
	require.NoError(t, err)
	assert.Nil(t, got, "missing key is nil, nil — not an error")
}

func TestLoadProgressReport_Error(t *testing.T) {
	t.Parallel()
	srv, client := newProgressRedis(t)
	srv.SetError("connection refused")

	got, err := LoadProgressReport(context.Background(), client, "wf-err")
	require.Error(t, err)
	assert.Nil(t, got)
}

// ----------------------------------------------------------------------------
// DeleteStaleProgressReport — the run-aware cleanup the API runs after a
// start is accepted.
// ----------------------------------------------------------------------------

func TestDeleteStaleProgressReport_KeepsLiveRun(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)
	ctx := context.Background()
	require.NoError(t, SaveProgressReport(ctx, client, "wf", "run-live", []byte("fresh")))

	deleted, err := DeleteStaleProgressReport(ctx, client, "wf", "run-live")
	require.NoError(t, err)
	assert.False(t, deleted)

	got, err := LoadProgressReport(ctx, client, "wf")
	require.NoError(t, err)
	assert.Equal(t, []byte("fresh"), got, "the live run's report must survive cleanup")
}

func TestDeleteStaleProgressReport_RemovesPreviousRun(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)
	ctx := context.Background()
	require.NoError(t, SaveProgressReport(ctx, client, "wf", "run-old", []byte("stale")))

	deleted, err := DeleteStaleProgressReport(ctx, client, "wf", "run-new")
	require.NoError(t, err)
	assert.True(t, deleted)

	got, err := LoadProgressReport(ctx, client, "wf")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// A report without a run stamp (activity-published shape) is by definition
// not the live run's — cleanup removes it.
func TestDeleteStaleProgressReport_RemovesUnstamped(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)
	ctx := context.Background()
	require.NoError(t, SaveProgressReport(ctx, client, "wf", "", []byte("unstamped")))

	deleted, err := DeleteStaleProgressReport(ctx, client, "wf", "run-new")
	require.NoError(t, err)
	assert.True(t, deleted)
}

func TestDeleteStaleProgressReport_MissingKeyIsNoop(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)

	deleted, err := DeleteStaleProgressReport(context.Background(), client, "wf-none", "run-new")
	require.NoError(t, err)
	assert.False(t, deleted)
}

func TestDeleteStaleProgressReport_RejectsEmptyLiveRun(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)
	ctx := context.Background()
	require.NoError(t, SaveProgressReport(ctx, client, "wf", "", []byte("unstamped")))

	_, err := DeleteStaleProgressReport(ctx, client, "wf", "")
	require.Error(t, err, "an empty live run would match unstamped reports and keep them")

	got, err := LoadProgressReport(ctx, client, "wf")
	require.NoError(t, err)
	assert.Equal(t, []byte("unstamped"), got, "rejected call must not touch the key")
}

func TestDeleteStaleProgressReport_Error(t *testing.T) {
	t.Parallel()
	srv, client := newProgressRedis(t)
	srv.SetError("connection refused")

	_, err := DeleteStaleProgressReport(context.Background(), client, "wf", "run-new")
	require.Error(t, err)
}

// ----------------------------------------------------------------------------
// Batch helpers
// ----------------------------------------------------------------------------

func TestDeleteProgressReportBatch_Empty(t *testing.T) {
	t.Parallel()
	srv, client := newProgressRedis(t)
	srv.SetError("must not be called")

	// Empty slice must be a no-op: no Redis command issued.
	require.NoError(t, DeleteProgressReportBatch(context.Background(), client, []string{}))
}

func TestDeleteProgressReportBatch_NonEmpty(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)
	ctx := context.Background()
	for _, id := range []string{"wf-a", "wf-b", "wf-keep"} {
		require.NoError(t, SaveProgressReport(ctx, client, id, "run-x", []byte(id)))
	}

	require.NoError(t, DeleteProgressReportBatch(ctx, client, []string{"wf-a", "wf-b"}))

	for _, id := range []string{"wf-a", "wf-b"} {
		got, err := LoadProgressReport(ctx, client, id)
		require.NoError(t, err)
		assert.Nil(t, got, id)
	}
	got, err := LoadProgressReport(ctx, client, "wf-keep")
	require.NoError(t, err)
	assert.Equal(t, []byte("wf-keep"), got, "batch delete is unconditional but scoped to its IDs")
}

func TestLoadProgressReportBatch_Empty(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)

	result, err := LoadProgressReportBatch(context.Background(), client, []string{})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestLoadProgressReportBatch_MixedHitsAndMisses(t *testing.T) {
	t.Parallel()
	_, client := newProgressRedis(t)
	ctx := context.Background()
	require.NoError(t, SaveProgressReport(ctx, client, "wf-x", "run-1", []byte(`{"x":1}`)))
	require.NoError(t, SaveProgressReport(ctx, client, "wf-y", "run-1", []byte(`{"y":2}`)))

	result, err := LoadProgressReportBatch(ctx, client, []string{"wf-x", "wf-miss", "wf-y"})
	require.NoError(t, err)
	assert.Equal(t, map[string][]byte{
		"wf-x": []byte(`{"x":1}`),
		"wf-y": []byte(`{"y":2}`),
	}, result, "missing keys are omitted, not errors")
}

func TestLoadProgressReportBatch_RedisDown(t *testing.T) {
	t.Parallel()
	srv, client := newProgressRedis(t)
	srv.SetError("connection refused")

	// A non-redis.Nil pipeline error with no readable results must surface as an
	// error, not an empty map that reads as "no reports exist".
	result, err := LoadProgressReportBatch(context.Background(), client, []string{"wf-down"})
	require.Error(t, err)
	assert.Nil(t, result)
}
