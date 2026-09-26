package cmd

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ────────────────────────────────────────────────────────────────────────────
// buildRefreshNewsInput
// ────────────────────────────────────────────────────────────────────────────

func TestBuildRefreshNewsInput_Default(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	input := buildRefreshNewsInput()

	assert.Nil(t, input.Force)
	assert.Empty(t, input.Sources)
}

func TestBuildRefreshNewsInput_ForceAndSources(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagNewsForce, true)
	viper.Set(config.FlagNewsOnly, " a, ,b ")

	input := buildRefreshNewsInput()

	require.NotNil(t, input.Force)
	assert.True(t, *input.Force)
	assert.Equal(t, []string{"a", "b"}, input.Sources)
}

// ────────────────────────────────────────────────────────────────────────────
// loadNewsDigest
// ────────────────────────────────────────────────────────────────────────────

const newsTestNHLPlayerID int64 = 8471214

// fakeNewsReportQueries is a minimal in-memory news.ReportQueries: one fetch
// state, one recently-reported incident, one player incident (with its own
// evidence) and one mention issue.
type fakeNewsReportQueries struct {
	fetchStates     []sqlcdb.NewsFetchState
	recentIncidents []sqlcdb.NewsIncident
	playerIncidents []sqlcdb.NewsIncident
	evidence        []sqlcdb.ListNewsIncidentEvidenceRow
	mentionIssues   []sqlcdb.ListNewsMentionIssuesRow
	err             error
}

func newsTS(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func newFakeNewsReportQueries(now time.Time) *fakeNewsReportQueries {
	return &fakeNewsReportQueries{
		fetchStates: []sqlcdb.NewsFetchState{
			{SourceID: "a", Scope: news.ScopeFeed, LastSuccessAt: newsTS(now)},
		},
		recentIncidents: []sqlcdb.NewsIncident{
			{ID: 1, PlayerName: "Recent Player", Category: "injury", FirstReportedAt: newsTS(now), LastReportedAt: newsTS(now)},
		},
		playerIncidents: []sqlcdb.NewsIncident{
			{ID: 2, NhlPlayerID: pgtype.Int8{Int64: newsTestNHLPlayerID, Valid: true}, PlayerName: "Sidney Crosby",
				Category: "injury", FirstReportedAt: newsTS(now), LastReportedAt: newsTS(now)},
		},
		evidence: []sqlcdb.ListNewsIncidentEvidenceRow{
			{IncidentID: 1, Relation: "independent", Publisher: "A", Kind: "official", VersionID: 10, Version: 1,
				Title: "Recent title", Url: "https://a.example/story", ReportedAt: newsTS(now)},
			{IncidentID: 2, Relation: "independent", Publisher: "B", Kind: "reporting", VersionID: 20, Version: 1,
				Title: "Crosby update", Url: "https://b.example/story", ReportedAt: newsTS(now)},
		},
		mentionIssues: []sqlcdb.ListNewsMentionIssuesRow{
			{Mention: "J. Smith", Resolution: "ambiguous", Method: "name", Candidates: []byte("[]"),
				Title: "Ambiguous story", Url: "https://a.example/amb", RetrievedAt: newsTS(now), Publisher: "A"},
		},
	}
}

func (f *fakeNewsReportQueries) ListNewsFetchStates(context.Context) ([]sqlcdb.NewsFetchState, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.fetchStates, nil
}

func (f *fakeNewsReportQueries) ListRecentNewsIncidents(context.Context, sqlcdb.ListRecentNewsIncidentsParams) ([]sqlcdb.NewsIncident, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.recentIncidents, nil
}

func (f *fakeNewsReportQueries) ListNewsIncidentsForPlayer(context.Context, sqlcdb.ListNewsIncidentsForPlayerParams) ([]sqlcdb.NewsIncident, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.playerIncidents, nil
}

func (f *fakeNewsReportQueries) ListNewsIncidentEvidence(_ context.Context, ids []int64) ([]sqlcdb.ListNewsIncidentEvidenceRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []sqlcdb.ListNewsIncidentEvidenceRow
	for _, e := range f.evidence {
		if slices.Contains(ids, e.IncidentID) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeNewsReportQueries) ListNewsMentionIssues(context.Context, sqlcdb.ListNewsMentionIssuesParams) ([]sqlcdb.ListNewsMentionIssuesRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.mentionIssues, nil
}

func newsTestRequest(player news.Identity) newsReportRequest {
	return newsReportRequest{
		Season: 2026, Since: 14 * hoursPerDay * time.Hour, Limit: 10,
		Sources: []news.Source{
			{ID: "a", Publisher: "A", Kind: news.KindOfficial, Adapter: news.AdapterRSS, URL: "https://a.example/feed", RefreshMinutes: 30, Enabled: true},
			{ID: "b", Publisher: "B", Kind: news.KindReporting, Adapter: news.AdapterRSS, URL: "https://b.example/feed", RefreshMinutes: 30, Enabled: true},
		},
		Player: player,
	}
}

func TestLoadNewsDigest_NoPlayerIDsLeavesPlayerNil(t *testing.T) {
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	fake := newFakeNewsReportQueries(now)
	request := newsTestRequest(news.Identity{})

	digest, err := loadNewsDigest(t.Context(), fake, request, now)

	require.NoError(t, err)
	assert.Len(t, digest.Coverage, len(request.Sources))
	assert.Equal(t, now.Add(-request.Since), digest.Since)
	assert.Nil(t, digest.Player)
	require.Len(t, digest.Incidents, 1)
	assert.Equal(t, "Recent Player", digest.Incidents[0].PlayerName)
	assert.Len(t, digest.Incidents[0].Evidence, 1)
	require.Len(t, digest.Issues, 1)
	assert.Equal(t, "J. Smith", digest.Issues[0].Mention)
}

func TestLoadNewsDigest_NHLIDSetsPlayerNamedFromFirstIncident(t *testing.T) {
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	fake := newFakeNewsReportQueries(now)
	request := newsTestRequest(news.Identity{NHLPlayerID: newsTestNHLPlayerID, Name: requestedPlayerName})

	digest, err := loadNewsDigest(t.Context(), fake, request, now)

	require.NoError(t, err)
	require.NotNil(t, digest.Player)
	assert.Equal(t, "Sidney Crosby", digest.Player.Player.Name)
	require.Len(t, digest.Player.Incidents, 1)
	assert.Equal(t, "Sidney Crosby", digest.Player.Incidents[0].PlayerName)
	assert.Len(t, digest.Player.Incidents[0].Evidence, 1)
}

func TestLoadNewsDigest_PropagatesQueryError(t *testing.T) {
	fake := &fakeNewsReportQueries{err: assert.AnError}

	_, err := loadNewsDigest(t.Context(), fake, newsTestRequest(news.Identity{}), time.Now())

	require.Error(t, err)
}

// ────────────────────────────────────────────────────────────────────────────
// ensureNewsSchedule
// ────────────────────────────────────────────────────────────────────────────

func TestAllSyncSteps_RefreshNewsBeforeFetchAssets(t *testing.T) {
	newsIdx := slices.Index(config.AllSyncSteps, config.StepRefreshNews)
	assetsIdx := slices.Index(config.AllSyncSteps, config.StepFetchAssets)

	require.GreaterOrEqual(t, newsIdx, 0)
	require.GreaterOrEqual(t, assetsIdx, 0)
	assert.Less(t, newsIdx, assetsIdx)
}

func TestWorkflowRefreshNews_String(t *testing.T) {
	assert.Equal(t, "refreshNews", workflowRefreshNews.String())
}

// ────────────────────────────────────────────────────────────────────────────
// news events / news eval flags
// ────────────────────────────────────────────────────────────────────────────

func TestNewsEventsRequestFromFlags(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	viper.Set(config.FlagNewsSinceDays, 3)
	viper.Set(config.FlagNewsLimit, 7)
	viper.Set(config.FlagNewsPlayerNHLID, 8475188)
	viper.Set(config.FlagNewsExtractMaxAttempts, 4)

	req, err := newsEventsRequestFromFlags(now)

	require.NoError(t, err)
	assert.Equal(t, now.Add(-3*24*time.Hour), req.Since)
	assert.Equal(t, 7, req.Limit)
	assert.Equal(t, 4, req.MaxAttempts)
	assert.Equal(t, int64(8475188), req.Player.NHLPlayerID)
}

func TestNewsEventsRequestFromFlagsRejectsNonPositiveWindow(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagNewsLimit, 7)

	_, err := newsEventsRequestFromFlags(time.Now())

	assert.Error(t, err)
}

func TestNewsExtractorFromFlags(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagNewsExtractProvider, "Ollama")
	viper.Set(config.FlagNewsExtractModel, "qwen3:8b")

	x, err := newsExtractorFromFlags()

	require.NoError(t, err)
	assert.Equal(t, "ollama/qwen3:8b/"+newsevent.PromptVersion+"/"+newsevent.SchemaVersion, x.Key())

	viper.Set(config.FlagNewsExtractProvider, "gemini")
	_, err = newsExtractorFromFlags()
	assert.Error(t, err)
}
