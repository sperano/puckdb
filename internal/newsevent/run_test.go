package newsevent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// Fixture constants shared by the Runner.Run tests below. One recurring
// scenario -- a team placing a player on injured reserve with a day-to-day
// injury, announced in a release -- is reused so the reply JSON and the
// article text that must support it only need to be written once.
const (
	testPublisher    = "Metro Beat"
	testArticleTitle = "Moose place Vale on injured reserve"
	testArticleText  = "The Metropolitan Moose placed forward Jordan Vale on injured reserve Thursday " +
		"with a day-to-day lower-body injury, per a team release."
	testPlayerNHLID   = int64(9900001)
	testPlayerMention = "Jordan Vale"
	testProvider      = "test-provider"
	testModelA        = "model-a"
	testModelB        = "model-b"
	testWindow        = 14 * 24 * time.Hour
	testMaxInputRunes = 5000
)

// injuryReply is a reference reply the model gives for testArticleText: it
// follows the output schema and every quote is verbatim in the article.
const injuryReply = `{"events": [{"player": "P1", "type": "injury", "report_status": "confirmed",
  "attribution": "team release", "effective_from": null,
  "duration": {"kind": "day_to_day", "games": 0, "days": 0, "until": "", "quote": "day-to-day lower-body injury"},
  "change": null,
  "evidence": [{"doc": "E1", "quote": "placed forward Jordan Vale on injured reserve Thursday with a day-to-day lower-body injury"}]}]}`

var fixedNow = time.Date(2026, 1, 8, 12, 0, 0, 0, time.UTC)

// mkRow builds a ListNewsVersionsToExtractRow as ListNewsVersionsToExtract
// would return it. Body is left empty so DocumentText uses text as-is.
func mkRow(id, articleID int64, version int32, publisher string, published time.Time, title, text string) sqlcdb.ListNewsVersionsToExtractRow {
	return sqlcdb.ListNewsVersionsToExtractRow{
		ID: id, ArticleID: articleID, Version: version, Title: title, EvidenceText: text,
		Publisher: publisher, Kind: string(news.KindReporting), PublishedAt: news.Timestamptz(published),
	}
}

// seedInjuryVersion registers a version of the recurring injury article
// (see the package constants above) in db: its metadata and its one
// resolved mention.
func seedInjuryVersion(db *fakeDB, id, articleID int64, version int32, published time.Time) sqlcdb.ListNewsVersionsToExtractRow {
	row := mkRow(id, articleID, version, testPublisher, published, testArticleTitle, testArticleText)
	db.addVersionFromRow(row)
	db.setVersionPlayers(id, sqlcdb.ListNewsVersionPlayersRow{
		NhlPlayerID: pgInt8(testPlayerNHLID), Name: testPlayerMention, Role: "subject",
	})
	return row
}

func newTestRunner(db *fakeDB, model string) *Runner {
	return &Runner{
		Extractor: Extractor{Provider: testProvider, Model: model, MaxOutputTokens: 1024},
		Queries:   db, InTx: db.InTx, Window: testWindow, MaxInputRunes: testMaxInputRunes,
		Now: func() time.Time { return fixedNow },
	}
}

// scriptedResponse is one queued answer for scriptedClient.
type scriptedResponse struct {
	resp *llm.Response
	err  error
}

// scriptedClient answers Complete with its queued responses in order, and
// fails the test-visible way (an error) if called more often than scripted:
// tests assert on how many times the model was called.
type scriptedClient struct {
	mu    sync.Mutex
	calls int
	queue []scriptedResponse
}

func newScriptedClient(responses ...scriptedResponse) *scriptedClient {
	return &scriptedClient{queue: responses}
}

func (c *scriptedClient) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls >= len(c.queue) {
		return nil, fmt.Errorf("scriptedClient: unscripted call %d", c.calls+1)
	}
	r := c.queue[c.calls]
	c.calls++
	return r.resp, r.err
}

func (c *scriptedClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func replyResponse(content string) scriptedResponse {
	return scriptedResponse{resp: &llm.Response{Content: content, Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 5}}}
}

func toolCallResponse() scriptedResponse {
	return scriptedResponse{resp: &llm.Response{
		ToolCalls: []llm.ToolCall{{ID: "1", Type: "function", Function: llm.ToolCallFunction{Name: "lookup"}}},
		Usage:     &llm.Usage{PromptTokens: 10, CompletionTokens: 5},
	}}
}

func transportErrorResponse(msg string) scriptedResponse {
	return scriptedResponse{err: errors.New(msg)}
}

// testBudget is a minimal Budget: it allows exactly remaining more calls.
type testBudget struct {
	remaining int
	spent     llm.Usage
}

func (b *testBudget) Reserve() bool {
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

func (b *testBudget) Release() {
	b.remaining++
}

func (b *testBudget) Spend(u llm.Usage) {
	b.spent.PromptTokens += u.PromptTokens
	b.spent.CompletionTokens += u.CompletionTokens
}

func TestRun_FirstExtractionCreatesEvent(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	client := newScriptedClient(replyResponse(injuryReply))
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client
	budget := &testBudget{remaining: 1}

	result, err := rn.Run(context.Background(), row, budget)
	require.NoError(t, err)

	assert.Equal(t, ExtractionSucceeded, result.Status)
	assert.True(t, result.Called)
	assert.False(t, result.Cached)
	assert.Equal(t, 1, result.Events.Created)
	assert.Equal(t, 1, client.callCount())

	require.Equal(t, 1, db.eventCount())
	ev, ok := db.event(1)
	require.True(t, ok)
	assert.Equal(t, "injury", ev.EventType)
	assert.Equal(t, "confirmed", ev.ReportStatus)
	assert.Equal(t, "active", ev.Lifecycle)
	assert.Equal(t, testPlayerMention, ev.PlayerName)

	evidence := db.evidenceFor(1)
	require.Len(t, evidence, 1)
	assert.Equal(t, string(RelationSupports), evidence[0].Relation)
	assert.Contains(t, string(evidence[0].Quotes), "injured reserve")

	transitions := db.transitionsFor(1)
	require.Len(t, transitions, 1)
	assert.Equal(t, "", transitions[0].FromLifecycle)
	assert.Equal(t, "active", transitions[0].ToLifecycle)

	extractions := db.extractionsFor(row.ID)
	require.Len(t, extractions, 1)
	assert.Equal(t, string(ExtractionSucceeded), extractions[0].Status)
	assert.True(t, extractions[0].ReconciledAt.Valid)
}

func TestRun_CreatedEventLinksToConfiguredIncident(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	db.addIncident(row.ID, string(TypeInjury), testPlayerNHLID, 0, 777)
	client := newScriptedClient(replyResponse(injuryReply))
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client

	_, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)

	ev, ok := db.event(1)
	require.True(t, ok)
	require.True(t, ev.IncidentID.Valid)
	assert.Equal(t, int64(777), ev.IncidentID.Int64)
}

func TestRun_CrashBetweenFinishAndReconcile_ReconcilesWithoutModelCall(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	rn := newTestRunner(db, testModelA)
	client := newScriptedClient() // no responses queued: a call fails the test
	rn.Extractor.Client = client

	in, err := BuildInput(context.Background(), db, row, testMaxInputRunes)
	require.NoError(t, err)
	db.seedExtraction(sqlcdb.NewsExtraction{
		VersionID: row.ID, ExtractorKey: rn.Extractor.Key(), InputHash: InputHash(in),
		Status: string(ExtractionSucceeded), RawOutput: injuryReply, Attempts: 1,
	})

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)

	assert.Equal(t, ExtractionSucceeded, result.Status)
	assert.False(t, result.Called)
	assert.Equal(t, 0, client.callCount())
	assert.Equal(t, 1, result.Events.Created)
	require.Equal(t, 1, db.eventCount())

	extractions := db.extractionsFor(row.ID)
	require.Len(t, extractions, 1)
	assert.True(t, extractions[0].ReconciledAt.Valid)
}

func TestRun_IdenticalInputServedFromCache(t *testing.T) {
	db := newFakeDB()
	row1 := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	// Same title, text, publisher, report date and players, but a different
	// article and version: the rendered input, and so the input hash, match.
	row2 := seedInjuryVersion(db, 2, 200, 1, fixedNow)
	client := newScriptedClient(replyResponse(injuryReply))
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client
	budget := &testBudget{remaining: 2}

	result1, err := rn.Run(context.Background(), row1, budget)
	require.NoError(t, err)
	require.Equal(t, ExtractionSucceeded, result1.Status)

	result2, err := rn.Run(context.Background(), row2, budget)
	require.NoError(t, err)

	assert.Equal(t, ExtractionSucceeded, result2.Status)
	assert.False(t, result2.Called)
	assert.True(t, result2.Cached)
	assert.Equal(t, 1, client.callCount(), "the model must be called only once")
	assert.Equal(t, 0, result2.Events.Created, "the second report supports the existing event")
	assert.Equal(t, 1, result2.Events.Supported)

	require.Equal(t, 1, db.eventCount())
	evidence := db.evidenceFor(1)
	require.Len(t, evidence, 2)

	extractions := db.extractionsFor(row2.ID)
	require.Len(t, extractions, 1)
	assert.True(t, extractions[0].CachedFromID.Valid)
}

func TestRun_BudgetExhausted_Deferred(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	client := newScriptedClient() // any call fails the test
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 0})
	require.NoError(t, err)

	assert.Equal(t, ExtractionDeferred, result.Status)
	assert.False(t, result.Called)
	assert.Equal(t, 0, client.callCount())
	assert.Empty(t, db.extractionsFor(row.ID), "a deferred extraction must not be recorded as an attempt")
	assert.Zero(t, db.eventCount())
}

func TestRun_TransportError_Failed(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	untouched := seedExistingActiveEvent(db)
	client := newScriptedClient(transportErrorResponse("connection reset"))
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)

	assert.Equal(t, ExtractionFailed, result.Status)
	assert.Contains(t, result.Error, "connection reset")
	assert.Equal(t, 1, db.eventCount(), "no event was created by the failed run")

	extractions := db.extractionsFor(row.ID)
	require.Len(t, extractions, 1)
	assert.Equal(t, string(ExtractionFailed), extractions[0].Status)
	assert.Equal(t, int32(1), extractions[0].Attempts)
	assert.Contains(t, extractions[0].LastError, "connection reset")

	after, ok := db.event(untouched.ID)
	require.True(t, ok)
	assert.Equal(t, untouched, after, "an unrelated event on record must be untouched")
}

func TestRun_InvalidJSON_Invalid(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	client := newScriptedClient(replyResponse("this is not json"))
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)

	assert.Equal(t, ExtractionInvalid, result.Status)
	assert.Zero(t, db.eventCount())
	extractions := db.extractionsFor(row.ID)
	require.Len(t, extractions, 1)
	assert.Equal(t, string(ExtractionInvalid), extractions[0].Status)
	assert.Equal(t, "this is not json", extractions[0].RawOutput)
}

func TestRun_UnknownField_Invalid(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	client := newScriptedClient(replyResponse(`{"events": [], "fantasy_impact": "big"}`))
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)

	assert.Equal(t, ExtractionInvalid, result.Status)
	assert.Zero(t, db.eventCount())
}

func TestRun_ToolCallReply_Invalid(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	client := newScriptedClient(toolCallResponse())
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)

	assert.Equal(t, ExtractionInvalid, result.Status)
	assert.Contains(t, result.Error, "tool call")
	assert.Zero(t, db.eventCount())
	extractions := db.extractionsFor(row.ID)
	require.Len(t, extractions, 1)
	assert.Equal(t, string(ExtractionInvalid), extractions[0].Status)
}

func TestRun_NoResolvedPlayers_EmptyEventsNoModelCall(t *testing.T) {
	db := newFakeDB()
	row := mkRow(1, 100, 1, testPublisher, fixedNow, "Roundup: around the league", "Nothing about any tracked player today.")
	// No ListNewsVersionPlayers rows and no earlier-article event players:
	// BuildInput yields an empty player list.
	client := newScriptedClient() // any call fails the test
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)

	assert.Equal(t, ExtractionSucceeded, result.Status)
	assert.False(t, result.Called)
	assert.Equal(t, 0, client.callCount())
	assert.Zero(t, db.eventCount())

	extractions := db.extractionsFor(row.ID)
	require.Len(t, extractions, 1)
	assert.Equal(t, int32(1), extractions[0].Attempts, "the attempt is still recorded")
	assert.Equal(t, emptyReply, extractions[0].RawOutput)
	assert.Equal(t, int32(0), extractions[0].Events)
}

// TestRun_CorrectionRetractsEvent and TestRun_ModelChangeAddsEvidenceToExistingEvent
// exercise the two halves of the correction / re-extraction story: a later
// version of the same article that no longer reports the event retracts it,
// and re-extracting a version under a different model key only adds
// evidence to the matching event already on record, leaving an audit trail
// of one extraction row per extractor key.

func TestRun_CorrectionRetractsEvent(t *testing.T) {
	db := newFakeDB()
	articleID := int64(700)
	row1 := seedInjuryVersion(db, 1, articleID, 1, fixedNow)
	row2 := mkRow(2, articleID, 2, testPublisher, fixedNow.Add(24*time.Hour),
		"Update: Vale not on injured reserve after all", "The team never placed Vale on injured reserve; Thursday's report was wrong.")
	db.addVersionFromRow(row2)
	// version 2 names no player itself; the correction is read only because
	// ListNewsArticleEventPlayers supplies the player of the event version 1
	// supports.

	client := newScriptedClient(replyResponse(injuryReply), replyResponse(emptyReply))
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client
	budget := &testBudget{remaining: 2}

	_, err := rn.Run(context.Background(), row1, budget)
	require.NoError(t, err)
	require.Equal(t, 1, db.eventCount())

	result2, err := rn.Run(context.Background(), row2, budget)
	require.NoError(t, err)

	assert.Equal(t, ExtractionSucceeded, result2.Status)
	assert.Equal(t, 1, result2.Events.Retracted)

	ev, ok := db.event(1)
	require.True(t, ok)
	assert.Equal(t, "retracted", ev.Lifecycle)

	transitions := db.transitionsFor(1)
	require.Len(t, transitions, 2, "creation, then the retraction")
	assert.Equal(t, "active", transitions[1].FromLifecycle)
	assert.Equal(t, "retracted", transitions[1].ToLifecycle)

	evidence := db.evidenceFor(1)
	require.Len(t, evidence, 2)
	assert.Equal(t, string(RelationWithdraws), evidence[1].Relation)
	assert.Equal(t, row2.ID, evidence[1].VersionID)
}

func TestRun_ModelChangeAddsEvidenceToExistingEvent(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)

	rnA := newTestRunner(db, testModelA)
	clientA := newScriptedClient(replyResponse(injuryReply))
	rnA.Extractor.Client = clientA
	_, err := rnA.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)
	require.Equal(t, 1, db.eventCount())

	rnB := newTestRunner(db, testModelB)
	clientB := newScriptedClient(replyResponse(injuryReply))
	rnB.Extractor.Client = clientB
	result, err := rnB.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)

	assert.Equal(t, ExtractionSucceeded, result.Status)
	assert.True(t, result.Called, "a different model key is not served from the first model's cache")
	assert.Equal(t, 0, result.Events.Created)
	assert.Equal(t, 1, result.Events.Supported)

	assert.Equal(t, 1, db.eventCount(), "no duplicate event for the same claim")
	assert.Len(t, db.evidenceFor(1), 2)

	extractions := db.extractionsFor(row.ID)
	require.Len(t, extractions, 2, "one extraction row per extractor key: the audit trail")
	assert.NotEqual(t, extractions[0].ExtractorKey, extractions[1].ExtractorKey)
}

// seedExistingActiveEvent inserts an event unrelated to the fixtures above,
// so a test can assert a failed or deferred run left it alone.
func seedExistingActiveEvent(db *fakeDB) sqlcdb.NewsEvent {
	id, err := db.CreateNewsEvent(context.Background(), sqlcdb.CreateNewsEventParams{
		NhlPlayerID: pgInt8(4242424), PlayerName: "Other Player", EventType: "trade", ReportStatus: "confirmed",
		DurationKind: string(DurationUnknown), Lifecycle: string(LifecycleActive),
		FirstReportedAt: news.Timestamptz(fixedNow.Add(-48 * time.Hour)),
	})
	if err != nil {
		panic(err)
	}
	ev, _ := db.event(id)
	return ev
}
