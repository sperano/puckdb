package newsfeed

// PostgreSQL-backed tests of ExtractNewsEvents: the real migration and
// queries, with a scripted model standing in for the LLM. See
// activities_pg_helpers_test.go for the fixtures (one McNabb suspension
// reported by NHL.com, its team-site copy and RotoWire).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	pgExtractModel      = "scripted-v1"
	pgOtherModel        = "scripted-v2"
	pgFailingModel      = "scripted-down"
	pgExtractProvider   = "ollama"
	pgMcNabbVersions    = 3 // NHL.com story, its team-site copy, RotoWire
	pgExtractBatch      = 10
	pgExtractRetryMins  = 30
	pgExtractAttempts   = 2
	pgScriptedPrompt    = 100
	pgScriptedReplyToks = 20
	pgSuspensionGames   = 3
)

// pgMcNabbRef finds McNabb's player ref in a rendered input.
var pgMcNabbRef = regexp.MustCompile(`- (P\d+): Brayden McNabb`)

const pgNHLReply = `{"events": [{"player": "%s", "type": "suspension", "report_status": "confirmed",
  "attribution": "NHL Department of Player Safety", "effective_from": null,
  "duration": {"kind": "games", "games": 3, "days": 0, "until": "",
               "quote": "has been suspended for three preseason and regular-season games"},
  "change": null,
  "evidence": [{"doc": "E1", "quote": "Brayden McNabb has been suspended for three preseason and regular-season games"}]}]}`

const pgRotoWireReply = `{"events": [{"player": "%s", "type": "suspension", "report_status": "confirmed",
  "attribution": "Jesse Granger", "effective_from": null,
  "duration": {"kind": "games", "games": 3, "days": 0, "until": "", "quote": "McNabb was suspended three games"},
  "change": null,
  "evidence": [{"doc": "E1", "quote": "McNabb was suspended three games Monday by the NHL Department of Player Safety"}]}]}`

// scriptedLLM answers the McNabb stories as a correct model would.
type scriptedLLM struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *scriptedLLM) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	user := req.Messages[len(req.Messages)-1].Content
	reply := `{"events": []}`
	if m := pgMcNabbRef.FindStringSubmatch(user); m != nil {
		switch {
		case strings.Contains(user, "Department of Player Safety announced today"):
			reply = fmt.Sprintf(pgNHLReply, m[1])
		case strings.Contains(user, "per Jesse Granger"):
			reply = fmt.Sprintf(pgRotoWireReply, m[1])
		}
	}
	return &llm.Response{Content: reply, Usage: &llm.Usage{PromptTokens: pgScriptedPrompt, CompletionTokens: pgScriptedReplyToks}}, nil
}

func (s *scriptedLLM) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func useModel(acts *Activities, client llm.Client) {
	acts.LLM = func(llm.Provider, string, time.Duration) llm.Client { return client }
}

func extractInput(model string, calls int) ExtractInput {
	return ExtractInput{
		Provider: pgExtractProvider, Model: model, MaxOutputTokens: 1024, MaxInputChars: 24_000, TimeoutSeconds: 30,
		BatchSize: pgExtractBatch, Concurrency: 1, MaxAttempts: pgExtractAttempts, RetryMinutes: pgExtractRetryMins,
		LookbackDays: pgKeepAllDays, IncidentWindowHours: pgIncidentWindowHours, RemainingCalls: calls, RemainingTokens: 1_000_000,
	}
}

func extract(t *testing.T, env *testsuite.TestActivityEnvironment, acts *Activities, input ExtractInput) ExtractResult {
	t.Helper()
	val, err := env.ExecuteActivity(acts.ExtractNewsEvents, input)
	require.NoError(t, err)
	var result ExtractResult
	require.NoError(t, val.Get(&result))
	return result
}

func TestPGExtractNewsEventsCreatesOneEventFromRepeatedReports(t *testing.T) {
	pool := openNewsPGTestDB(t)
	acts, env := seedProcessedNews(t, pool)
	model := &scriptedLLM{}
	useModel(acts, model)

	first := extract(t, env, acts, extractInput(pgExtractModel, pgExtractBatch))
	assert.Equal(t, pgMcNabbVersions, first.Versions)
	assert.Equal(t, pgMcNabbVersions, first.Succeeded)
	assert.Equal(t, pgMcNabbVersions, first.Calls+first.Cached, "identical team-site copies may be served from the cache")
	assert.Equal(t, 1, first.Events.Created, "three reports of one suspension are one event")
	assert.Equal(t, pgMcNabbVersions-1, first.Events.Supported)
	assert.Zero(t, first.Issues)
	assertMcNabbEvent(t, pool, pgMcNabbVersions)

	again := extract(t, env, acts, extractInput(pgExtractModel, pgExtractBatch))
	assert.Zero(t, again.Versions, "extracted versions are not extracted again")
	calls := model.callCount()

	upgraded := extract(t, env, acts, extractInput(pgOtherModel, pgExtractBatch))
	assert.Equal(t, pgMcNabbVersions, upgraded.Versions, "a new model re-reads the same versions")
	assert.Zero(t, upgraded.Events.Created, "and only adds evidence to the event on record")
	assert.Greater(t, model.callCount(), calls)
	assertMcNabbEvent(t, pool, 2*pgMcNabbVersions)
	assert.Equal(t, 2*pgMcNabbVersions, countRows(t, pool, "news_extractions"), "each model keeps its own audit row")
}

func assertMcNabbEvent(t *testing.T, pool *pgxpool.Pool, supports int) {
	t.Helper()
	events, err := sqlcdb.New(pool).ListNewsEventsForPlayer(context.Background(), sqlcdb.ListNewsEventsForPlayerParams{
		NhlPlayerID: pgtype.Int8{Int64: pgMcNabbID, Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, string(newsevent.TypeSuspension), ev.EventType)
	assert.Equal(t, string(newsevent.StatusConfirmed), ev.ReportStatus)
	assert.Equal(t, string(newsevent.DurationGames), ev.DurationKind)
	assert.Equal(t, int32(pgSuspensionGames), ev.DurationGames.Int32)
	assert.Equal(t, string(newsevent.LifecycleActive), ev.Lifecycle)
	assert.True(t, ev.IncidentID.Valid, "the event links the incident candidate it came from")
	assert.False(t, ev.NeedsReview)
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM news_event_evidence WHERE event_id = $1 AND relation = 'supports'`, ev.ID).Scan(&n))
	assert.Equal(t, supports, n)
}

func TestPGExtractNewsEventsFailureKeepsEventsAndRetriesLater(t *testing.T) {
	pool := openNewsPGTestDB(t)
	acts, env := seedProcessedNews(t, pool)
	useModel(acts, &scriptedLLM{})
	extract(t, env, acts, extractInput(pgExtractModel, pgExtractBatch))

	down := &scriptedLLM{err: errors.New("provider unavailable")}
	useModel(acts, down)
	failed := extract(t, env, acts, extractInput(pgFailingModel, pgExtractBatch))
	assert.Equal(t, pgMcNabbVersions, failed.Failed)
	assertMcNabbEvent(t, pool, pgMcNabbVersions)

	soon := extract(t, env, acts, extractInput(pgFailingModel, pgExtractBatch))
	assert.Zero(t, soon.Versions, "a failed call waits for the retry delay")

	later, laterEnv := newNewsActivities(pool, nil, pgFixedNow.Add(2*pgExtractRetryMins*time.Minute))
	useModel(later, down)
	retried := extract(t, laterEnv, later, extractInput(pgFailingModel, pgExtractBatch))
	assert.Equal(t, pgMcNabbVersions, retried.Failed)

	exhausted, exhaustedEnv := newNewsActivities(pool, nil, pgFixedNow.Add(4*pgExtractRetryMins*time.Minute))
	useModel(exhausted, down)
	assert.Zero(t, extract(t, exhaustedEnv, exhausted, extractInput(pgFailingModel, pgExtractBatch)).Versions,
		"out of attempts, the versions are left for review")
	reviews, err := sqlcdb.New(pool).ListNewsExtractionReviews(context.Background(), sqlcdb.ListNewsExtractionReviewsParams{
		Since: news.Timestamptz(pgLoadSince), MaxAttempts: pgExtractAttempts, MaxRows: pgLargeBatch,
	})
	require.NoError(t, err)
	assert.Len(t, reviews, pgMcNabbVersions)
	assertMcNabbEvent(t, pool, pgMcNabbVersions)
}

func TestPGExtractNewsEventsStopsAtTheCallBudget(t *testing.T) {
	pool := openNewsPGTestDB(t)
	acts, env := seedProcessedNews(t, pool)
	model := &scriptedLLM{}
	useModel(acts, model)

	result := extract(t, env, acts, extractInput(pgExtractModel, 1))

	assert.Equal(t, 1, model.callCount())
	assert.Positive(t, result.Deferred)
	assert.False(t, result.Remaining, "a deferred version ends the refresh's extraction")
	assert.Equal(t, 1+result.Cached, countRows(t, pool, "news_extractions"),
		"the call and any cache hits record an attempt; deferred versions do not")
}

func TestPGExtractNewsEventsUnknownProviderFails(t *testing.T) {
	pool := openNewsPGTestDB(t)
	acts, env := newNewsActivities(pool, nil, pgFixedNow)
	useModel(acts, &scriptedLLM{})
	input := extractInput(pgExtractModel, pgExtractBatch)
	input.Provider = "gemini"
	_, err := env.ExecuteActivity(acts.ExtractNewsEvents, input)
	require.Error(t, err)
	assertApplicationErrorType(t, err, ErrTypeExtractConfig, true)
}

func TestPGPruneRemovesExpiredEvents(t *testing.T) {
	pool := openNewsPGTestDB(t)
	acts, env := seedProcessedNews(t, pool)
	useModel(acts, &scriptedLLM{})
	extract(t, env, acts, extractInput(pgExtractModel, pgExtractBatch))
	require.Equal(t, 1, countRows(t, pool, "news_events"))

	future, futureEnv := newNewsActivities(pool, nil, pgFixedNow.Add(pgFarFutureDays*hoursPerDay*time.Hour))
	val, err := futureEnv.ExecuteActivity(future.PruneNews, PruneInput{RetentionDays: 1, KeepVersions: 1})
	require.NoError(t, err)
	var pruned PruneResult
	require.NoError(t, val.Get(&pruned))
	assert.Equal(t, int64(1), pruned.Events)
	assert.Zero(t, countRows(t, pool, "news_events"))
	assert.Zero(t, countRows(t, pool, "news_articles"), "once no event backs them, old articles go too")
}

const pgDenialReply = `{"events": [{"player": "%s", "type": "suspension", "report_status": "denied",
  "attribution": "", "effective_from": null,
  "duration": {"kind": "unknown", "games": 0, "days": 0, "until": "", "quote": ""}, "change": null,
  "evidence": [{"doc": "E1", "quote": "Brayden McNabb was not suspended"}]}]}`

// correctingLLM answers the corrected McNabb story with a denial and every
// other story as scriptedLLM does.
type correctingLLM struct{ scriptedLLM }

func (c *correctingLLM) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	user := req.Messages[len(req.Messages)-1].Content
	if m := pgMcNabbRef.FindStringSubmatch(user); m != nil && strings.Contains(user, "was not suspended") {
		return &llm.Response{Content: fmt.Sprintf(pgDenialReply, m[1]), Usage: &llm.Usage{}}, nil
	}
	return c.scriptedLLM.Complete(ctx, req)
}

func TestPGExtractNewsEventsCorrectionRetractsTheEvent(t *testing.T) {
	pool := openNewsPGTestDB(t)
	seedNewsDirectory(t, pool)
	fake := newRSSFakeServer(t, readNewsTestdata(t, "nhl-player-safety.json"))
	acts, env := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
	src := defaultNewsSource(t, "nhl-player-safety", fake.url())
	fetchSource(t, env, acts, src)
	processBatch(t, env, acts, pgLargeBatch)
	useModel(acts, &correctingLLM{})
	require.Equal(t, 1, extract(t, env, acts, extractInput(pgExtractModel, pgExtractBatch)).Events.Created)

	fake.setBody(mutateNHLFixture(t, [2]string{
		"has been suspended for three preseason and regular-season games for cross-checking",
		"was not suspended and has been fined for cross-checking",
	}))
	fetchSource(t, env, acts, src)
	processBatch(t, env, acts, pgLargeBatch)
	corrected := extract(t, env, acts, extractInput(pgExtractModel, pgExtractBatch))

	assert.Positive(t, corrected.Versions, "the newer versions are read because their earlier versions back an event")
	assert.Equal(t, 1, corrected.Events.Retracted, "the official correction retracts the suspension")
	events, err := sqlcdb.New(pool).ListNewsEventsForPlayer(context.Background(), sqlcdb.ListNewsEventsForPlayerParams{
		NhlPlayerID: pgtype.Int8{Int64: pgMcNabbID, Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, string(newsevent.LifecycleRetracted), events[0].Lifecycle)
	transitions, err := sqlcdb.New(pool).ListNewsEventTransitions(context.Background(), []int64{events[0].ID})
	require.NoError(t, err)
	require.Len(t, transitions, 2, "creation and retraction are both on the audit trail")
	assert.Equal(t, string(newsevent.LifecycleRetracted), transitions[1].ToLifecycle)
}

func TestPGStartNewsExtractionLeavesARecentAttemptAlone(t *testing.T) {
	pool := openNewsPGTestDB(t)
	seedProcessedNews(t, pool)
	ctx := context.Background()
	var versionID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT min(id) FROM news_article_versions`).Scan(&versionID))
	q := sqlcdb.New(pool)
	start := func(at, staleBefore time.Time) (sqlcdb.StartNewsExtractionRow, error) {
		return q.StartNewsExtraction(ctx, sqlcdb.StartNewsExtractionParams{
			VersionID: versionID, ExtractorKey: pgExtractModel, Provider: pgExtractProvider, Model: pgExtractModel,
			PromptVersion: newsevent.PromptVersion, SchemaVersion: newsevent.SchemaVersion, InputHash: "h",
			LastAttemptAt: news.Timestamptz(at), StaleBefore: news.Timestamptz(staleBefore),
		})
	}
	retry := pgExtractRetryMins * time.Minute

	first, err := start(pgFixedNow, pgFixedNow.Add(-retry))
	require.NoError(t, err)
	assert.Equal(t, int32(1), first.Attempts)

	_, err = start(pgFixedNow.Add(time.Minute), pgFixedNow.Add(time.Minute-retry))
	assert.ErrorIs(t, err, pgx.ErrNoRows, "a concurrent refresh must not start a second call")

	later := pgFixedNow.Add(2 * retry)
	taken, err := start(later, later.Add(-retry))
	require.NoError(t, err, "a stale pending attempt (crashed worker) is taken over")
	assert.Equal(t, first.ID, taken.ID)
	assert.Equal(t, int32(2), taken.Attempts)
}
