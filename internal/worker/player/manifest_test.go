package player

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/temporal"
)

// boxscorePlayerTTL is the TTL the tests give the consolidated Redis list.
const boxscorePlayerTTL = 0

// fakeManifestStore is an in-memory ProcessPlayersStore.
type fakeManifestStore struct {
	mu        sync.Mutex
	refs      map[string]ManifestRef
	players   map[string][]store.BoxscorePlayer
	errors    map[string][]PlayerError
	finished  map[string]bool
	matched   []store.YahooPlayerID
	creations int
}

func newFakeManifestStore() *fakeManifestStore {
	return &fakeManifestStore{
		refs:     map[string]ManifestRef{},
		players:  map[string][]store.BoxscorePlayer{},
		errors:   map[string][]PlayerError{},
		finished: map[string]bool{},
	}
}

func (f *fakeManifestStore) GetManifest(_ context.Context, id string) (ManifestRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, ok := f.refs[id]
	if !ok {
		return ManifestRef{}, ErrManifestNotFound
	}
	return ref, nil
}

func (f *fakeManifestStore) CreateManifest(_ context.Context, id, _ string, players []store.BoxscorePlayer, hash []byte) (ManifestRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creations++
	if ref, ok := f.refs[id]; ok {
		return ref, nil
	}
	ref := newManifestRef(id, len(players), hash)
	f.refs[id] = ref
	f.players[id] = players
	return ref, nil
}

func (f *fakeManifestStore) ManifestPlayers(_ context.Context, id string, from, to int) ([]store.BoxscorePlayer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	players := f.players[id]
	return players[from:min(to, len(players))], nil
}

func (f *fakeManifestStore) RecordErrors(_ context.Context, id string, errs []PlayerError) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.refs[id]; !ok {
		return ErrManifestNotFound
	}
	f.errors[id] = append(f.errors[id], errs...)
	return nil
}

func (f *fakeManifestStore) FinishManifest(_ context.Context, id string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.refs[id]; !ok {
		return 0, ErrManifestNotFound
	}
	f.finished[id] = true
	return len(f.errors[id]), nil
}

func (f *fakeManifestStore) MatchedYahooIDs(context.Context) ([]store.YahooPlayerID, error) {
	return f.matched, nil
}

func TestSortManifestPlayers_OrdersByIDAndKeepsFirstOfRepeats(t *testing.T) {
	got := sortManifestPlayers([]store.BoxscorePlayer{
		{ID: 3, FirstName: "C"}, {ID: 1, FirstName: "A"}, {ID: 3, FirstName: "C2"}, {ID: 2, FirstName: "B"},
	})
	assert.Equal(t, []store.BoxscorePlayer{{ID: 1, FirstName: "A"}, {ID: 2, FirstName: "B"}, {ID: 3, FirstName: "C"}}, got)
}

func TestHashManifestPlayers(t *testing.T) {
	players := []store.BoxscorePlayer{{ID: 1, FirstName: "Ab", LastName: "C", Position: "D"}}
	assert.Equal(t, hashManifestPlayers(players), hashManifestPlayers(players), "stable")

	split := []store.BoxscorePlayer{{ID: 1, FirstName: "A", LastName: "bC", Position: "D"}}
	assert.NotEqual(t, hashManifestPlayers(players), hashManifestPlayers(split),
		"the same characters split differently across fields hash differently")
	reordered := []store.BoxscorePlayer{players[0], {ID: 2}}
	assert.NotEqual(t, hashManifestPlayers(reordered), hashManifestPlayers([]store.BoxscorePlayer{{ID: 2}, players[0]}),
		"order is part of the hash")
}

type ManifestActivitySuite struct {
	ActivitySuite
	redis *redis.Client
	store *fakeManifestStore
	act   *Activities
}

func TestManifestActivitySuite(t *testing.T) {
	suite.Run(t, new(ManifestActivitySuite))
}

func (s *ManifestActivitySuite) SetupTest() {
	s.ActivitySuite.SetupTest()
	s.redis = redis.NewClient(&redis.Options{Addr: miniredis.RunT(s.T()).Addr()})
	s.store = newFakeManifestStore()
	s.act = &Activities{Storage: store.NewMemStorage(), RedisClient: s.redis, Manifests: s.store, NHLClient: &MockNHLClient{}}
	s.env.RegisterActivity(s.act)
}

func (s *ManifestActivitySuite) saveRedisPlayers(players ...store.BoxscorePlayer) {
	require.NoError(s.T(), cache.SaveAllBoxscorePlayers(context.Background(), s.redis, players, boxscorePlayerTTL))
}

func (s *ManifestActivitySuite) createManifest(id string) ManifestRef {
	future, err := s.env.ExecuteActivity(s.act.CreateProcessPlayersManifest, CreateManifestInput{ManifestID: id, WorkflowID: "wf"})
	require.NoError(s.T(), err)
	var ref ManifestRef
	require.NoError(s.T(), future.Get(&ref))
	return ref
}

func (s *ManifestActivitySuite) TestCreate_CopiesTheRedisListOrderedByID() {
	s.saveRedisPlayers(store.BoxscorePlayer{ID: 30}, store.BoxscorePlayer{ID: 10}, store.BoxscorePlayer{ID: 20})

	ref := s.createManifest("run-1")

	want := []store.BoxscorePlayer{{ID: 10}, {ID: 20}, {ID: 30}}
	s.Equal(want, s.store.players["run-1"])
	s.Equal(newManifestRef("run-1", len(want), hashManifestPlayers(want)), ref)
}

func (s *ManifestActivitySuite) TestCreate_ReturnsAnExistingManifestWithoutReadingRedis() {
	s.saveRedisPlayers(store.BoxscorePlayer{ID: 1}, store.BoxscorePlayer{ID: 2})
	first := s.createManifest("run-1")
	s.saveRedisPlayers(store.BoxscorePlayer{ID: 9})

	s.Equal(first, s.createManifest("run-1"), "a retry keeps the list the chain started with")
	s.Equal(1, s.store.creations)
}

func (s *ManifestActivitySuite) TestCreate_EmptyRedisListStoresNothing() {
	s.Equal(ManifestRef{ID: "run-1"}, s.createManifest("run-1"))
	s.Zero(s.store.creations)
}

// putManifest stores players as manifest id directly.
func (s *ManifestActivitySuite) putManifest(id string, players ...store.BoxscorePlayer) ManifestRef {
	ref, err := s.store.CreateManifest(context.Background(), id, "wf", players, hashManifestPlayers(players))
	require.NoError(s.T(), err)
	return ref
}

// requireManifestMissing asserts err is the non-retryable manifest error.
func (s *ManifestActivitySuite) requireManifestMissing(err error, contains string) {
	var appErr *temporal.ApplicationError
	s.Require().ErrorAs(err, &appErr)
	s.Equal(ManifestMissingErrorType, appErr.Type())
	s.True(appErr.NonRetryable())
	s.Contains(appErr.Error(), contains)
}

func (s *ManifestActivitySuite) TestBatch_RejectsAManifestThatIsGoneOrChanged() {
	ref := s.putManifest("run-1", store.BoxscorePlayer{ID: 1}, store.BoxscorePlayer{ID: 2})

	_, err := s.env.ExecuteActivity(s.act.ProcessPlayerManifestBatch, ManifestBatchInput{
		Manifest: ManifestRef{ID: "gone", Count: 2, Hash: ref.Hash}, From: 0, To: 1})
	s.requireManifestMissing(err, "not found")

	changed := ref
	changed.Hash = "other"
	_, err = s.env.ExecuteActivity(s.act.ProcessPlayerManifestBatch, ManifestBatchInput{Manifest: changed, From: 0, To: 1})
	s.requireManifestMissing(err, "changed")

	_, err = s.env.ExecuteActivity(s.act.ProcessPlayerManifestBatch, ManifestBatchInput{Manifest: ref, From: 1, To: 3})
	var appErr *temporal.ApplicationError
	s.Require().ErrorAs(err, &appErr)
	s.Equal(InvalidManifestWindowErrorType, appErr.Type())
	s.True(appErr.NonRetryable())
}

func (s *ManifestActivitySuite) TestBatch_RejectsAShortWindow() {
	ref := s.putManifest("run-1", store.BoxscorePlayer{ID: 1}, store.BoxscorePlayer{ID: 2})
	s.store.players["run-1"] = s.store.players["run-1"][:1]

	_, err := s.env.ExecuteActivity(s.act.ProcessPlayerManifestBatch, ManifestBatchInput{Manifest: ref, From: 0, To: 2})
	s.requireManifestMissing(err, "holds 1 players")
}

// writeMissingLanding marks p's landing as missing (404), so processing it
// only upserts the boxscore data.
func (s *ManifestActivitySuite) writeMissingLanding(p store.BoxscorePlayer) {
	data, err := json.Marshal(store.MissingPlayerLandingData{FirstName: p.FirstName, LastName: p.LastName, Position: p.Position})
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.act.Storage.Write(context.Background(),
		resource.MissingPlayerLanding{PlayerID: nhl.PlayerID(p.ID)}.Path(), data))
}

func (s *ManifestActivitySuite) TestBatch_ProcessesItsWindowAndReturnsErrorsWithTheirStage() {
	players := []store.BoxscorePlayer{
		{ID: 1, FirstName: "Not", LastName: "Mine", Position: "C"},
		{ID: 2, FirstName: "Fails", LastName: "Upsert", Position: "C"},
		{ID: 3, FirstName: "Good", LastName: "Player", Position: "D"},
	}
	ref := s.putManifest("run-1", players...)
	for _, p := range players {
		s.writeMissingLanding(p)
	}
	upserter := NewMockPlayerUpserter()
	upserter.On("UpsertPlayer", mock.Anything, mock.MatchedBy(func(p sqlcdb.UpsertPlayerParams) bool { return p.ID == 2 })).
		Return(errors.New("boom"))
	upserter.On("UpsertPlayer", mock.Anything, mock.MatchedBy(func(p sqlcdb.UpsertPlayerParams) bool { return p.ID == 3 })).
		Return(nil)
	s.act.Queries = upserter

	future, err := s.env.ExecuteActivity(s.act.ProcessPlayerManifestBatch, ManifestBatchInput{Manifest: ref, From: 1, To: 3})
	s.Require().NoError(err)
	var result ManifestBatchResult
	s.Require().NoError(future.Get(&result))

	s.Equal(2, result.Missing)
	s.Equal(1, result.Imported)
	s.Nil(result.Errors, "manifest batches report errors only with their stage")
	s.Equal([]PlayerError{{PlayerID: 2, Stage: StageMissingImport, Message: "player 2 (missing): import error: boom"}},
		result.PlayerErrors)
	upserter.AssertExpectations(s.T())
	upserter.AssertNumberOfCalls(s.T(), "UpsertPlayer", 2)
}

func (s *ManifestActivitySuite) TestBatch_RebuildsAPoolLostDuringTheExecution() {
	player := store.BoxscorePlayer{ID: 1, FirstName: "Only", LastName: "Player", Position: "C"}
	ref := s.putManifest("run-1", player)
	s.writeMissingLanding(player)
	require.NoError(s.T(), s.act.Storage.Write(context.Background(),
		resource.YahooPlayer{PlayerID: 97}.Path(), []byte(sampleYahooPlayerHTML)))
	upserter := NewMockPlayerUpserter()
	upserter.On("UpsertPlayer", mock.Anything, mock.Anything).Return(nil)
	s.act.Queries = upserter

	_, err := s.env.ExecuteActivity(s.act.ProcessPlayerManifestBatch,
		ManifestBatchInput{Manifest: ref, From: 0, To: 1, YahooPoolSize: 1})
	s.Require().NoError(err)

	pool, err := LoadYahooIDPool(context.Background(), s.redis)
	s.Require().NoError(err)
	s.Len(pool, 1, "the batch rebuilt the expired pool before matching")
}

func (s *ManifestActivitySuite) TestBatch_RebuildsAPartialPool() {
	player := store.BoxscorePlayer{ID: 1, FirstName: "Only", LastName: "Player", Position: "C"}
	ref := s.putManifest("run-1", player)
	s.writeMissingLanding(player)
	require.NoError(s.T(), saveYahooPlayersToRedisImplForTest(s.redis, 97))
	for _, id := range []store.YahooPlayerID{97, 29} {
		require.NoError(s.T(), s.act.Storage.Write(context.Background(),
			resource.YahooPlayer{PlayerID: id}.Path(), []byte(sampleYahooPlayerHTML)))
	}
	upserter := NewMockPlayerUpserter()
	upserter.On("UpsertPlayer", mock.Anything, mock.Anything).Return(nil)
	s.act.Queries = upserter

	_, err := s.env.ExecuteActivity(s.act.ProcessPlayerManifestBatch,
		ManifestBatchInput{Manifest: ref, From: 0, To: 1, YahooPoolSize: 2})
	s.Require().NoError(err)

	pool, err := LoadYahooIDPool(context.Background(), s.redis)
	s.Require().NoError(err)
	s.Len(pool, 2, "a pool read while another batch rebuilds it is completed first")
}

// saveYahooPlayersToRedisImplForTest saves a pool of the given Yahoo IDs.
func saveYahooPlayersToRedisImplForTest(client *redis.Client, ids ...store.YahooPlayerID) error {
	players := make([]store.YahooPlayer, len(ids))
	for i, id := range ids {
		players[i] = store.YahooPlayer{YahooID: id}
	}
	_, err := saveYahooPlayersToRedisImpl(context.Background(), client, players)
	return err
}

func (s *ManifestActivitySuite) TestBatch_LegacyBatchesDoNotRebuildThePool() {
	require.NoError(s.T(), s.act.Storage.Write(context.Background(),
		resource.YahooPlayer{PlayerID: 97}.Path(), []byte(sampleYahooPlayerHTML)))

	pool, err := s.act.loadYahooIDPool(context.Background(), 0)
	s.Require().NoError(err)
	s.Empty(pool)
}

func (s *ManifestActivitySuite) TestRecordAndFinish() {
	s.putManifest("run-1", store.BoxscorePlayer{ID: 1})
	errs := []PlayerError{{PlayerID: 1, Stage: StageRead, Message: "read error"}}

	_, err := s.env.ExecuteActivity(s.act.RecordProcessPlayersErrors, RecordErrorsInput{ManifestID: "run-1", Errors: errs})
	s.Require().NoError(err)
	s.Equal(errs, s.store.errors["run-1"])

	future, err := s.env.ExecuteActivity(s.act.FinishProcessPlayersManifest, "run-1")
	s.Require().NoError(err)
	var count int
	s.Require().NoError(future.Get(&count))
	s.Equal(1, count)
	s.True(s.store.finished["run-1"])

	_, err = s.env.ExecuteActivity(s.act.RecordProcessPlayersErrors, RecordErrorsInput{ManifestID: "gone", Errors: errs})
	s.requireManifestMissing(err, "not found")
	_, err = s.env.ExecuteActivity(s.act.FinishProcessPlayersManifest, "gone")
	s.requireManifestMissing(err, "not found")
}
