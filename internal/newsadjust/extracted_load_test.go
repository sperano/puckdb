package newsadjust

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reconcileWindow is how far apart two reports of one event may be.
const reconcileWindow = 14 * hoursPerDay * time.Hour

// fakeExtractedQueries serves one stored suspension and records which
// extractors the gate was asked about.
type fakeExtractedQueries struct {
	events      []sqlcdb.ListNewsEventsForAdjustmentRow
	evidence    []sqlcdb.ListNewsEventAdjustmentEvidenceRow
	transitions []sqlcdb.NewsEventTransition
	passed      map[string]bool
	params      sqlcdb.ListNewsEventsForAdjustmentParams
}

func (f *fakeExtractedQueries) ListNewsEventsForAdjustment(_ context.Context, arg sqlcdb.ListNewsEventsForAdjustmentParams) ([]sqlcdb.ListNewsEventsForAdjustmentRow, error) {
	f.params = arg
	return f.events, nil
}

func (f *fakeExtractedQueries) ListNewsEventAdjustmentEvidence(context.Context, []int64) ([]sqlcdb.ListNewsEventAdjustmentEvidenceRow, error) {
	return f.evidence, nil
}

func (f *fakeExtractedQueries) ListNewsEventTransitions(context.Context, []int64) ([]sqlcdb.NewsEventTransition, error) {
	return f.transitions, nil
}

func (f *fakeExtractedQueries) ListNewsAdjustmentTeams(context.Context) ([]sqlcdb.ListNewsAdjustmentTeamsRow, error) {
	return []sqlcdb.ListNewsAdjustmentTeamsRow{{Abbrev: "WPG", FullName: "Winnipeg Jets", CommonName: "Jets", TeamID: testJetsID}}, nil
}

func (f *fakeExtractedQueries) InsertNewsExtractionEvaluation(context.Context, sqlcdb.InsertNewsExtractionEvaluationParams) (int64, error) {
	return 0, nil
}

func (f *fakeExtractedQueries) GetLatestNewsExtractionEvaluation(_ context.Context, arg sqlcdb.GetLatestNewsExtractionEvaluationParams) (sqlcdb.NewsExtractionEvaluation, error) {
	passed, exists := f.passed[arg.ExtractorKey]
	if !exists {
		return sqlcdb.NewsExtractionEvaluation{}, pgx.ErrNoRows
	}
	return sqlcdb.NewsExtractionEvaluation{Passed: passed}, nil
}

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func fakeSuspension(id int64, extractor string) *fakeExtractedQueries {
	quotes, _ := json.Marshal([]newsevent.Quote{{Doc: "D1", Text: "suspended indefinitely"}})
	return &fakeExtractedQueries{
		events: []sqlcdb.ListNewsEventsForAdjustmentRow{{
			NewsEvent: sqlcdb.NewsEvent{
				ID: id, NhlPlayerID: pgtype.Int8{Int64: testGoalieNHLID, Valid: true}, PlayerName: "Goalie Fixture",
				EventType: "suspension", ReportStatus: "confirmed", DurationKind: "indefinite", Lifecycle: "active",
			},
			ExtractorKey: extractor,
		}},
		evidence: []sqlcdb.ListNewsEventAdjustmentEvidenceRow{{
			EventID: id, VersionID: testEvidenceID, ExtractionID: 1, Relation: "supports", Publisher: "NHL.com",
			Kind: "official", ReportedAt: pgTime(testReportedAt), Quotes: quotes, AddedAt: pgTime(testReportedAt.Add(time.Hour)),
			RetrievedAt: pgTime(testReportedAt), Url: "https://www.nhl.com/news/story",
		}},
		transitions: []sqlcdb.NewsEventTransition{{
			EventID: id, ToLifecycle: "active", VersionID: pgtype.Int8{Int64: testEvidenceID, Valid: true},
			ExtractionID: pgtype.Int8{Int64: 1, Valid: true}, At: pgTime(testReportedAt.Add(time.Hour)),
		}},
		passed: map[string]bool{testExtractor: true},
	}
}

func TestLoadExtractedEvents_MapsRowsAndAsksTheReleaseGate(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	pool := testSkater(testPoolKey)
	q := fakeSuspension(testSuspensionID, testExtractor)
	events, warnings, err := LoadExtractedEvents(context.Background(), q, []projection.PlayerProjection{goalie, pool})
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, []int64{testGoalieNHLID}, q.params.NhlPlayerIds)
	assert.Equal(t, []int32{testYahooID}, q.params.YahooPlayerIds)
	require.Len(t, events, 1)
	e := events[0]
	assert.Equal(t, "news-event:1", e.ID)
	assert.Equal(t, testGoalieKey, e.PlayerKey)
	assert.Equal(t, EventSuspension, e.Type)
	assert.Empty(t, e.Hold)
	assert.Equal(t, "suspended indefinitely", e.Evidence[0].Quote)
	assert.Equal(t, testReportedAt.Add(time.Hour), e.RecordedAt)

	unevaluated := fakeSuspension(testSuspensionID, "never/evaluated")
	events, _, err = LoadExtractedEvents(context.Background(), unevaluated, []projection.PlayerProjection{goalie})
	require.NoError(t, err)
	assert.Contains(t, events[0].Hold, "has not passed its evaluation")
}

// reconcileReport stores an article version and its extraction, then
// reconciles the report's events through the extraction package, as a
// refresh does.
func reconcileReport(t *testing.T, pool *pgxpool.Pool, externalID string, reportedAt time.Time, events ...newsevent.Event) {
	t.Helper()
	ctx := context.Background()
	var articleID, versionID, extractionID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO news_articles (publisher, external_id, source_id, kind, url, first_seen_at, last_seen_at)
		VALUES ('NHL.com', $1, 'nhl-content', 'official', 'https://www.nhl.com/news/' || $1, $2, $2) RETURNING id`,
		externalID, reportedAt).Scan(&articleID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO news_article_versions (article_id, version, content_hash, title_fingerprint,
		text_fingerprint, title, evidence_text, url, published_at, retrieved_at)
		VALUES ($1, 1, $2, $2, $2, 'Story', 'Story text', 'https://www.nhl.com/news/' || $2, $3, $3) RETURNING id`,
		articleID, externalID, reportedAt).Scan(&versionID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO news_extractions (version_id, extractor_key, provider, model, prompt_version,
		schema_version, input_hash, status) VALUES ($1, $2, 'anthropic', 'model', 'prompt-v1', 'schema-v1', $3, 'succeeded') RETURNING id`,
		versionID, testExtractor, externalID).Scan(&extractionID))

	report := newsevent.Report{
		VersionID: versionID, ArticleID: articleID, Version: 1, Publisher: "NHL.com", Kind: news.KindOfficial,
		ReportedAt: reportedAt, ExtractionID: extractionID, Clean: true,
	}
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlcdb.New(tx)
	existing, err := newsevent.LoadNear(ctx, q, []news.Identity{goalieIdentity()}, report, reconcileWindow)
	require.NoError(t, err)
	plan := newsevent.Reconcile(existing, report, events, reconcileWindow)
	_, err = newsevent.Apply(ctx, q, plan, report, time.Now())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func TestRepository_LoadEventsReplaysReconciledExtraction(t *testing.T) {
	pool := openAdjustmentTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE news_articles, news_extraction_evaluations CASCADE`)
	require.NoError(t, err)

	quote := func(text string) []newsevent.Quote { return []newsevent.Quote{{Doc: "D1", Text: text}} }
	reconcileReport(t, pool, "suspension", testReportedAt, newsevent.Event{
		Player: goalieIdentity(), Type: newsevent.TypeSuspension, Status: newsevent.StatusConfirmed,
		Duration: newsevent.Duration{Kind: newsevent.DurationIndefinite}, Evidence: quote("suspended indefinitely"),
	})
	between := time.Now()
	reconcileReport(t, pool, "reinstatement", testReinstatedAt, newsevent.Event{
		Player: goalieIdentity(), Type: newsevent.TypeReinstatement, Status: newsevent.StatusConfirmed,
		Duration: newsevent.Duration{Kind: newsevent.DurationUnknown}, Evidence: quote("has been reinstated"),
	})
	corpus, err := newsevent.LoadCorpus("")
	require.NoError(t, err)
	_, err = sqlcdb.New(pool).InsertNewsExtractionEvaluation(ctx, sqlcdb.InsertNewsExtractionEvaluationParams{
		ExtractorKey: testExtractor, CorpusVersion: corpus.Version, Cases: 1, Passed: true, Metrics: []byte(`{}`), RunAt: pgTime(between),
	})
	require.NoError(t, err)

	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	baseline := testBaseline(goalie)
	events, warnings, err := NewRepository(pool).LoadEvents(ctx, baseline)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Len(t, events, 3, "suspension created and resolved, reinstatement created")

	before := testRequest(baseline, events...)
	before.AsOf = between
	assert.Positive(t, missed(mustApply(t, before), testGoalieKey, ScenarioBase))
	after := testRequest(baseline, events...)
	after.AsOf = time.Now()
	result := mustApply(t, after)
	for _, s := range Scenarios {
		assert.Zero(t, missed(result, testGoalieKey, s), s)
	}
}
