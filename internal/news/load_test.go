package news

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// erroringReportQueries wraps a fakeStore, injecting an error from one
// chosen method while delegating everything else.
type erroringReportQueries struct {
	*fakeStore
	fetchStatesErr   error
	recentErr        error
	evidenceErr      error
	mentionIssuesErr error
}

func (e *erroringReportQueries) ListNewsFetchStates(ctx context.Context) ([]sqlcdb.NewsFetchState, error) {
	if e.fetchStatesErr != nil {
		return nil, e.fetchStatesErr
	}
	return e.fakeStore.ListNewsFetchStates(ctx)
}

func (e *erroringReportQueries) ListRecentNewsIncidents(ctx context.Context, arg sqlcdb.ListRecentNewsIncidentsParams) ([]sqlcdb.NewsIncident, error) {
	if e.recentErr != nil {
		return nil, e.recentErr
	}
	return e.fakeStore.ListRecentNewsIncidents(ctx, arg)
}

func (e *erroringReportQueries) ListNewsIncidentEvidence(ctx context.Context, ids []int64) ([]sqlcdb.ListNewsIncidentEvidenceRow, error) {
	if e.evidenceErr != nil {
		return nil, e.evidenceErr
	}
	return e.fakeStore.ListNewsIncidentEvidence(ctx, ids)
}

func (e *erroringReportQueries) ListNewsMentionIssues(ctx context.Context, arg sqlcdb.ListNewsMentionIssuesParams) ([]sqlcdb.ListNewsMentionIssuesRow, error) {
	if e.mentionIssuesErr != nil {
		return nil, e.mentionIssuesErr
	}
	return e.fakeStore.ListNewsMentionIssues(ctx, arg)
}

func TestFetchStateFromRowMapsEveryColumn(t *testing.T) {
	row := sqlcdb.NewsFetchState{
		SourceID: "rotowire-nhl", Scope: ScopeFeed, Publisher: "RotoWire", Etag: "etag", LastModified: "lm", BodyHash: "hash",
		LastAttemptAt: ts(retrievedAt), LastSuccessAt: ts(retrievedAt.Add(-time.Hour)),
		DataAsOf: ts(retrievedAt.Add(-2 * time.Hour)), LastFailureAt: ts(retrievedAt.Add(-3 * time.Hour)),
		LastError: "boom", ConsecutiveFailures: 2, LastItems: 10, LastNewVersions: 3,
	}
	got := FetchStateFromRow(row)
	assert.Equal(t, FetchState{
		SourceID: "rotowire-nhl", Scope: ScopeFeed,
		LastAttemptAt: retrievedAt, LastSuccessAt: retrievedAt.Add(-time.Hour),
		DataAsOf: retrievedAt.Add(-2 * time.Hour), LastFailureAt: retrievedAt.Add(-3 * time.Hour),
		LastError: "boom", ConsecutiveFailures: 2, LastItems: 10, LastNewVersions: 3,
	}, got)
}

func TestLoadFetchStatesMapsAllRows(t *testing.T) {
	store := newFakeStore()
	store.states = []sqlcdb.NewsFetchState{
		{SourceID: "a", Scope: ScopeFeed, LastAttemptAt: ts(retrievedAt)},
		{SourceID: "b", Scope: "season:2026", LastAttemptAt: ts(retrievedAt)},
	}
	got, err := LoadFetchStates(context.Background(), store)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].SourceID)
	assert.Equal(t, "b", got[1].SourceID)
}

func TestLoadFetchStatesWrapsQueryError(t *testing.T) {
	boom := errors.New("boom")
	_, err := LoadFetchStates(context.Background(), &erroringReportQueries{fakeStore: newFakeStore(), fetchStatesErr: boom})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "list news fetch states:")
}

// mentionIssueVersion adds an unresolved subject mention on a fresh article
// version so ListNewsMentionIssues (and LoadMentionIssues) has a row to read.
func mentionIssueVersion(t *testing.T, store *fakeStore, candidates []byte) int64 {
	t.Helper()
	ctx := context.Background()
	articleID, err := store.UpsertNewsArticle(ctx, sqlcdb.UpsertNewsArticleParams{
		Publisher: "RotoWire", ExternalID: "mention-issue", SourceID: "rotowire-nhl", Kind: string(KindReporting),
		FirstSeenAt: ts(retrievedAt),
	})
	require.NoError(t, err)
	versionID, err := store.InsertNewsArticleVersion(ctx, sqlcdb.InsertNewsArticleVersionParams{
		ArticleID: articleID, Version: 1, ContentHash: "h", Title: "Elias Pettersson update", RetrievedAt: ts(retrievedAt),
	})
	require.NoError(t, err)
	require.NoError(t, store.InsertNewsMention(ctx, sqlcdb.InsertNewsMentionParams{
		VersionID: versionID, Mention: "elias pettersson", Role: string(RoleSubject),
		Resolution: string(ResolutionAmbiguous), Method: string(MethodName), Candidates: candidates,
	}))
	return versionID
}

func TestLoadMentionIssuesDecodesCandidates(t *testing.T) {
	store := newFakeStore()
	candidates, err := json.Marshal([]Identity{{Name: "Elias Pettersson", NHLPlayerID: petterssonCID, Team: "VAN"}})
	require.NoError(t, err)
	mentionIssueVersion(t, store, candidates)

	issues, err := LoadMentionIssues(context.Background(), store, retrievedAt.Add(-time.Hour), defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	require.Len(t, issues[0].Candidates, 1)
	assert.Equal(t, "Elias Pettersson", issues[0].Candidates[0].Name)
	assert.Equal(t, int64(petterssonCID), issues[0].Candidates[0].NHLPlayerID)
}

func TestLoadMentionIssuesFailsOnInvalidCandidateJSON(t *testing.T) {
	store := newFakeStore()
	mentionIssueVersion(t, store, []byte("not json"))

	_, err := LoadMentionIssues(context.Background(), store, retrievedAt.Add(-time.Hour), defaultMaxItems)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode candidates of mention")
}

func TestLoadMentionIssuesWrapsQueryError(t *testing.T) {
	boom := errors.New("boom")
	_, err := LoadMentionIssues(context.Background(), &erroringReportQueries{fakeStore: newFakeStore(), mentionIssuesErr: boom},
		retrievedAt.Add(-time.Hour), defaultMaxItems)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "list news mention issues:")
}

func TestLoadRecentIncidentsWrapsEvidenceError(t *testing.T) {
	store := newFakeStore()
	_, err := store.CreateNewsIncident(context.Background(), sqlcdb.CreateNewsIncidentParams{
		PlayerName: "Alex Lyon", Category: string(CategoryInjury), FirstReportedAt: ts(retrievedAt),
	})
	require.NoError(t, err)

	boom := errors.New("boom")
	_, err = LoadRecentIncidents(context.Background(), &erroringReportQueries{fakeStore: store, evidenceErr: boom},
		retrievedAt.Add(-time.Hour), defaultMaxItems)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "list news incident evidence:")
}

func TestLoadRecentIncidentsWrapsQueryError(t *testing.T) {
	boom := errors.New("boom")
	_, err := LoadRecentIncidents(context.Background(), &erroringReportQueries{fakeStore: newFakeStore(), recentErr: boom},
		retrievedAt.Add(-time.Hour), defaultMaxItems)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "list recent news incidents:")
}

// twoIncidentStore builds two incidents, each with its own article version
// attached as evidence, so evidence-to-incident attachment can be checked.
func twoIncidentStore(t *testing.T) *fakeStore {
	t.Helper()
	ctx := context.Background()
	store := newFakeStore()

	articleA, err := store.UpsertNewsArticle(ctx, sqlcdb.UpsertNewsArticleParams{
		Publisher: "NHL.com", ExternalID: "a", SourceID: "nhl-injury", Kind: string(KindOfficial), FirstSeenAt: ts(retrievedAt),
	})
	require.NoError(t, err)
	versionA, err := store.InsertNewsArticleVersion(ctx, sqlcdb.InsertNewsArticleVersionParams{
		ArticleID: articleA, Version: 1, ContentHash: "hashA", Title: "McNabb suspended", RetrievedAt: ts(retrievedAt),
	})
	require.NoError(t, err)

	articleB, err := store.UpsertNewsArticle(ctx, sqlcdb.UpsertNewsArticleParams{
		Publisher: "RotoWire", ExternalID: "b", SourceID: "rotowire-nhl", Kind: string(KindReporting), FirstSeenAt: ts(retrievedAt),
	})
	require.NoError(t, err)
	versionB, err := store.InsertNewsArticleVersion(ctx, sqlcdb.InsertNewsArticleVersionParams{
		ArticleID: articleB, Version: 1, ContentHash: "hashB", Title: "Lyon hurt", RetrievedAt: ts(retrievedAt),
	})
	require.NoError(t, err)

	incA, err := store.CreateNewsIncident(ctx, sqlcdb.CreateNewsIncidentParams{
		NhlPlayerID: pgtype.Int8{Int64: mcnabbID, Valid: true}, PlayerName: "Brayden McNabb",
		Category: string(CategorySuspension), FirstReportedAt: ts(retrievedAt),
	})
	require.NoError(t, err)
	incB, err := store.CreateNewsIncident(ctx, sqlcdb.CreateNewsIncidentParams{
		NhlPlayerID: pgtype.Int8{Int64: lyonID, Valid: true}, PlayerName: "Alex Lyon",
		Category: string(CategoryInjury), FirstReportedAt: ts(retrievedAt),
	})
	require.NoError(t, err)

	require.NoError(t, store.InsertNewsIncidentEvidence(ctx, sqlcdb.InsertNewsIncidentEvidenceParams{
		IncidentID: incA, VersionID: versionA, ArticleID: articleA, Publisher: "NHL.com", Kind: string(KindOfficial),
		ReportedAt: ts(retrievedAt), Relation: string(RelationIndependent),
	}))
	require.NoError(t, store.InsertNewsIncidentEvidence(ctx, sqlcdb.InsertNewsIncidentEvidenceParams{
		IncidentID: incB, VersionID: versionB, ArticleID: articleB, Publisher: "RotoWire", Kind: string(KindReporting),
		ReportedAt: ts(retrievedAt), Relation: string(RelationIndependent),
	}))
	return store
}

func TestLoadRecentIncidentsAttachesEvidenceToTheRightIncidents(t *testing.T) {
	store := twoIncidentStore(t)
	incidents, err := LoadRecentIncidents(context.Background(), store, retrievedAt.Add(-time.Hour), defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, incidents, 2)

	byPlayer := map[int64]Incident{}
	for _, inc := range incidents {
		byPlayer[inc.NHLPlayerID] = inc
	}
	require.Contains(t, byPlayer, int64(mcnabbID))
	require.Len(t, byPlayer[int64(mcnabbID)].Evidence, 1)
	assert.Equal(t, "McNabb suspended", byPlayer[int64(mcnabbID)].Evidence[0].Title)

	require.Contains(t, byPlayer, int64(lyonID))
	require.Len(t, byPlayer[int64(lyonID)].Evidence, 1)
	assert.Equal(t, "Lyon hurt", byPlayer[int64(lyonID)].Evidence[0].Title)
}
