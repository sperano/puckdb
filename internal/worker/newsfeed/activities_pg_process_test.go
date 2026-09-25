package newsfeed

// PostgreSQL-backed tests of ProcessNewsVersions and PruneNews. See
// activities_pg_helpers_test.go for fixtures and activities_pg_test.go for
// PlanNewsRefresh/FetchNewsSource/RecordNewsFetchFailure.

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPGProcessNewsVersionsCreatesIncidentsAndMentions(t *testing.T) {
	pool := openNewsPGTestDB(t)
	seedNewsDirectory(t, pool)
	nhlSrv := staticServer(t, http.StatusOK, readNewsTestdata(t, "nhl-player-safety.json"))
	rssSrv := staticServer(t, http.StatusOK, readNewsTestdata(t, "rotowire.xml"))
	acts, env := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
	nhlResult := fetchSource(t, env, acts, defaultNewsSource(t, "nhl-player-safety", nhlSrv.URL))
	assert.Equal(t, news.IngestResult{Items: 3, NewVersions: 3}, nhlResult.Stored)
	rssResult := fetchSource(t, env, acts, defaultNewsSource(t, "rotowire-nhl", rssSrv.URL))
	assert.Equal(t, news.IngestResult{Items: 5, NewVersions: 5}, rssResult.Stored)

	first := processBatch(t, env, acts, pgTotalSeedVersions)
	assert.True(t, first.Remaining, "a batch exactly as large as the queue still reports more may be waiting")
	assert.Equal(t, pgTotalSeedVersions, first.Outcome.Versions)

	second := processBatch(t, env, acts, pgLargeBatch)
	assert.False(t, second.Remaining, "a batch larger than the (now empty) queue has nothing left waiting")
	assert.Zero(t, second.Outcome.Versions, "running again finds nothing left to process")

	assertMcNabbIncident(t, pool)
	assertPetterssonAmbiguous(t, pool)
}

func assertMcNabbIncident(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	q := sqlcdb.New(pool)
	ctx := context.Background()
	playerNews, err := news.LoadPlayerNews(ctx, q, news.Identity{NHLPlayerID: pgMcNabbID}, nil)
	require.NoError(t, err)
	require.Len(t, playerNews.Incidents, 1, "the league story, its team-site copy and RotoWire's report are one suspension")
	inc := playerNews.Incidents[0]
	assert.Equal(t, news.CategorySuspension, inc.Category)
	assert.Equal(t, []string{"NHL.com", "RotoWire"}, inc.IndependentPublishers())

	recent, err := news.LoadRecentIncidents(ctx, q, pgLoadSince, pgLargeBatch)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	assert.Equal(t, inc.ID, recent[0].ID)
}

func assertPetterssonAmbiguous(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	q := sqlcdb.New(pool)
	ctx := context.Background()
	for _, id := range []int64{pgPetterssonAID, pgPetterssonBID} {
		playerNews, err := news.LoadPlayerNews(ctx, q, news.Identity{NHLPlayerID: id}, nil)
		require.NoError(t, err)
		assert.Empty(t, playerNews.Incidents, "two Elias Petterssons play for Vancouver: the mention stays ambiguous")
	}
	issues, err := news.LoadMentionIssues(ctx, q, pgLoadSince, pgLargeBatch)
	require.NoError(t, err)
	var found *news.MentionIssue
	for i := range issues {
		if issues[i].Mention == "elias pettersson" {
			found = &issues[i]
		}
	}
	require.NotNil(t, found, "the ambiguous Pettersson mention is kept for review")
	assert.Equal(t, news.ResolutionAmbiguous, found.Resolution)
	assert.Len(t, found.Candidates, 2)
}

func TestPGPruneFarFutureDeletesIncidentsAndArticles(t *testing.T) {
	pool := openNewsPGTestDB(t)
	acts, _ := seedProcessedNews(t, pool)

	articlesBefore := countRows(t, pool, "news_articles")
	require.Positive(t, articlesBefore)
	incidentsBefore := countRows(t, pool, "news_incidents")
	require.Positive(t, incidentsBefore)

	future := pgFixedNow.Add(pgFarFutureDays * hoursPerDay * time.Hour)
	pruneActs := &Activities{Queries: acts.Queries, Now: func() time.Time { return future }}
	result, err := pruneActs.PruneNews(context.Background(), PruneInput{RetentionDays: 1, KeepVersions: pgLargeBatch})
	require.NoError(t, err)

	assert.EqualValues(t, incidentsBefore, result.Incidents)
	assert.EqualValues(t, articlesBefore, result.Articles)
	assert.Zero(t, countRows(t, pool, "news_incidents"))
	assert.Zero(t, countRows(t, pool, "news_articles"))
}

// TestPGPruneKeepsEvidenceVersions corrects the official McNabb suspension
// story once (so its old version stays evidence for the incident) while
// Cristall's unrelated, never-newsworthy fine story is corrected twice (so
// its old versions back no incident and are free to prune).
func TestPGPruneKeepsEvidenceVersions(t *testing.T) {
	pool := openNewsPGTestDB(t)
	seedNewsDirectory(t, pool)
	fake := newRSSFakeServer(t, readNewsTestdata(t, "nhl-player-safety.json"))
	acts, env := newNewsActivities(pool, fake.srv.Client(), pgFixedNow)
	src := defaultNewsSource(t, "nhl-player-safety", fake.url())

	bodies := [][]byte{
		mutateNHLFixture(t),
		mutateNHLFixture(t, [2]string{"fined $2,000", "fined $3,000"}),
		mutateNHLFixture(t, [2]string{"fined $2,000", "fined $5,000"}),
		mutateNHLFixture(t, [2]string{"fined $2,000", "fined $5,000"}, [2]string{"three preseason", "two preseason"}),
	}
	for _, body := range bodies {
		fake.setBody(body)
		fetchSource(t, env, acts, src)
		processBatch(t, env, acts, pgLargeBatch)
	}

	mcnabbVersions := countVersionsFor(t, pool, pgNHLContentPublisher, pgMcNabbOfficialEntityID)
	require.Equal(t, 2, mcnabbVersions, "the official suspension story was corrected once")

	result, err := acts.PruneNews(context.Background(), PruneInput{RetentionDays: pgKeepAllDays, KeepVersions: 1})
	require.NoError(t, err)

	assert.EqualValues(t, 2, result.Versions, "Cristall's two superseded, evidence-free versions are removed")
	assert.Equal(t, mcnabbVersions, countVersionsFor(t, pool, pgNHLContentPublisher, pgMcNabbOfficialEntityID),
		"the evidence-backed old version of a corrected story is kept despite being old")
	assert.Equal(t, 1, countVersionsFor(t, pool, pgNHLContentPublisher, pgCristallEntityID),
		"only Cristall's latest correction remains")
	assert.Positive(t, countRows(t, pool, "news_incidents"), "the suspension incident itself is untouched")
}

// pgConcurrentRefreshes is how many refreshes process the same queue at once
// (a scheduled and an on-demand run, and then some).
const (
	pgConcurrentRefreshes = 4
	// pgMcNabbEvidence: the league story, its team-site copy and RotoWire.
	pgMcNabbEvidence = 3
)

func TestPGConcurrentProcessingCreatesOneIncidentPerEvent(t *testing.T) {
	pool := openNewsPGTestDB(t)
	seedNewsDirectory(t, pool)
	nhlSrv := staticServer(t, http.StatusOK, readNewsTestdata(t, "nhl-player-safety.json"))
	rssSrv := staticServer(t, http.StatusOK, readNewsTestdata(t, "rotowire.xml"))
	acts, env := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
	fetchSource(t, env, acts, defaultNewsSource(t, "nhl-player-safety", nhlSrv.URL))
	fetchSource(t, env, acts, defaultNewsSource(t, "rotowire-nhl", rssSrv.URL))

	errs := make(chan error, pgConcurrentRefreshes)
	var wg sync.WaitGroup
	for range pgConcurrentRefreshes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runActs, runEnv := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
			_, err := runEnv.ExecuteActivity(runActs.ProcessNewsVersions, ProcessInput{
				Season: pgYahooSeason, BatchSize: pgLargeBatch, IncidentWindowHours: pgIncidentWindowHours,
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assertMcNabbIncident(t, pool)
	assert.Equal(t, pgMcNabbEvidence, countRows(t, pool, "news_incident_evidence"),
		"each report is attached once however many refreshes processed it")
}
