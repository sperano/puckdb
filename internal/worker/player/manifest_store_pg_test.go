package player

// PostgreSQL-backed coverage for PgProcessPlayersStore. Each test runs on a
// private database (simfixtures.NewDatabase), so the retention deletes never
// touch rows another test relies on. Skips unless PUCKDB_TEST_PG_URL is set.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// concurrentAttempts is how many overlapping attempts the convergence tests
// start at once.
const concurrentAttempts = 4

func newPgManifestStore(t *testing.T) (*PgProcessPlayersStore, *pgxpool.Pool) {
	t.Helper()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	return NewPgProcessPlayersStore(pool), pool
}

// createPgManifest stores players, sorted, as manifest id.
func createPgManifest(t *testing.T, s *PgProcessPlayersStore, id string, players ...store.BoxscorePlayer) ManifestRef {
	t.Helper()
	sorted := sortManifestPlayers(players)
	ref, err := s.CreateManifest(context.Background(), id, "process-players", sorted, hashManifestPlayers(sorted))
	require.NoError(t, err)
	return ref
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func TestPgManifest_StoresPlayersInOrdinalOrder(t *testing.T) {
	s, _ := newPgManifestStore(t)
	ctx := context.Background()
	players := []store.BoxscorePlayer{
		{ID: 30, FirstName: "C", LastName: "Three", Position: "D"},
		{ID: 10, FirstName: "A", LastName: "One", Position: "C"},
		{ID: 20, FirstName: "B", LastName: "Two", Position: ""},
	}

	ref := createPgManifest(t, s, "run-1", players...)
	sorted := sortManifestPlayers(players)
	assert.Equal(t, newManifestRef("run-1", len(players), hashManifestPlayers(sorted)), ref)

	got, err := s.GetManifest(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, ref, got)

	window, err := s.ManifestPlayers(ctx, "run-1", 1, 3)
	require.NoError(t, err)
	assert.Equal(t, sorted[1:3], window, "[from, to) in player ID order")
	empty, err := s.ManifestPlayers(ctx, "run-1", 3, 5)
	require.NoError(t, err)
	assert.Empty(t, empty)
	_, err = s.ManifestPlayers(ctx, "run-1", 2, 1)
	assert.Error(t, err)

	_, err = s.GetManifest(ctx, "missing")
	assert.ErrorIs(t, err, ErrManifestNotFound)
}

func TestPgManifest_CreatingAnExistingIDKeepsTheFirstManifest(t *testing.T) {
	s, pool := newPgManifestStore(t)
	first := createPgManifest(t, s, "run-1", store.BoxscorePlayer{ID: 1}, store.BoxscorePlayer{ID: 2})

	again := createPgManifest(t, s, "run-1", store.BoxscorePlayer{ID: 7})

	assert.Equal(t, first, again)
	assert.Equal(t, 2, countRows(t, pool, "process_players_manifest_players"))
}

func TestPgManifest_ConcurrentCreationsConverge(t *testing.T) {
	s, pool := newPgManifestStore(t)
	refs := make([]ManifestRef, concurrentAttempts)
	errs := make([]error, concurrentAttempts)
	var wg sync.WaitGroup
	for i := range concurrentAttempts {
		wg.Go(func() {
			// Each attempt read a different list: whichever commits first wins.
			players := []store.BoxscorePlayer{{ID: 1}, {ID: int64(100 + i)}}
			refs[i], errs[i] = s.CreateManifest(context.Background(), "run-1", "process-players", players, hashManifestPlayers(players))
		})
	}
	wg.Wait()

	for i := range concurrentAttempts {
		require.NoError(t, errs[i])
		assert.Equal(t, refs[0], refs[i], "every attempt returns the committed manifest")
	}
	assert.Equal(t, 1, countRows(t, pool, "process_players_manifests"))
	assert.Equal(t, 2, countRows(t, pool, "process_players_manifest_players"))
}

func TestPgManifest_RetentionKeepsTheNewest(t *testing.T) {
	s, pool := newPgManifestStore(t)
	ctx := context.Background()
	for i := range ProcessPlayersManifestRetention + 1 {
		id := fmt.Sprintf("run-%d", i)
		createPgManifest(t, s, id, store.BoxscorePlayer{ID: 1})
		require.NoError(t, s.RecordErrors(ctx, id, []PlayerError{{PlayerID: 1, Stage: StageRead, Message: "x"}}))
	}

	_, err := s.GetManifest(ctx, "run-0")
	assert.ErrorIs(t, err, ErrManifestNotFound, "the oldest manifest is deleted")
	for i := 1; i <= ProcessPlayersManifestRetention; i++ {
		_, err := s.GetManifest(ctx, fmt.Sprintf("run-%d", i))
		assert.NoError(t, err)
	}
	assert.Equal(t, ProcessPlayersManifestRetention, countRows(t, pool, "process_players_manifest_players"),
		"its players go with it")
	assert.Equal(t, ProcessPlayersManifestRetention, countRows(t, pool, "process_players_manifest_errors"),
		"its errors go with it")
}

func TestPgManifest_ErrorsConvergeAndFinishCountsThem(t *testing.T) {
	s, pool := newPgManifestStore(t)
	ctx := context.Background()
	createPgManifest(t, s, "run-1", store.BoxscorePlayer{ID: 1}, store.BoxscorePlayer{ID: 2})
	errs := []PlayerError{
		{PlayerID: 1, Stage: StageRead, Message: "first"},
		{PlayerID: 1, Stage: StageRead, Message: "repeated"},
		{PlayerID: 1, Stage: StageCareer, Message: "career"},
		{PlayerID: 2, Stage: StageUpsert, Message: "upsert"},
	}

	results := make([]error, concurrentAttempts)
	var wg sync.WaitGroup
	for i := range concurrentAttempts {
		wg.Go(func() { results[i] = s.RecordErrors(ctx, "run-1", errs) })
	}
	wg.Wait()
	for _, err := range results {
		require.NoError(t, err)
	}

	assert.Equal(t, 3, countRows(t, pool, "process_players_manifest_errors"), "one row per player and stage")
	var message string
	require.NoError(t, pool.QueryRow(ctx, `SELECT message FROM process_players_manifest_errors
		WHERE manifest_id = 'run-1' AND player_id = 1 AND stage = 'read'`).Scan(&message))
	assert.Equal(t, "repeated", message)

	count, err := s.FinishManifest(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, 3, count)
	var finished bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT finished_at IS NOT NULL FROM process_players_manifests WHERE id = 'run-1'`).
		Scan(&finished))
	assert.True(t, finished)
}

func TestPgManifest_MissingManifest(t *testing.T) {
	s, _ := newPgManifestStore(t)
	ctx := context.Background()

	err := s.RecordErrors(ctx, "gone", []PlayerError{{PlayerID: 1, Stage: StageRead, Message: "x"}})
	assert.ErrorIs(t, err, ErrManifestNotFound)
	_, err = s.FinishManifest(ctx, "gone")
	assert.ErrorIs(t, err, ErrManifestNotFound)
}

func TestPgManifest_MatchedYahooIDs(t *testing.T) {
	s, pool := newPgManifestStore(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO players (id, first_name, last_name, yahoo_id) VALUES
		(8400001, 'A', 'One', 501), (8400002, 'B', 'Two', NULL)`)
	require.NoError(t, err)

	matched, err := s.MatchedYahooIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []store.YahooPlayerID{501}, matched)
}
