package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recentNewsTestSince = "2026-10-16T00:00:00Z"

var recentNewsColumns = []string{
	"event_id", "nhl_player_id", "yahoo_player_id", "player_name", "event_type", "report_status", "attribution",
	"effective_from", "duration_kind", "duration_games", "duration_days", "duration_until", "change_field", "change_from",
	"change_to", "lifecycle", "superseded_by", "lifecycle_reason", "first_reported_at", "last_reported_at", "needs_review", "review_reason", "evidence_relation",
	"evidence_publisher", "evidence_kind", "evidence_reported_at", "evidence_title", "evidence_url", "evidence_quote",
}

type recentNewsQueryFake struct {
	eventCalls    int
	evidenceCalls int
	params        sqlcdb.ListRecentNewsEventsParams
	evidenceIDs   []int64
	events        []sqlcdb.NewsEvent
	evidence      []sqlcdb.ListNewsEventEvidenceRow
	eventErr      error
	evidenceErr   error
}

func (f *recentNewsQueryFake) ListRecentNewsEvents(_ context.Context, params sqlcdb.ListRecentNewsEventsParams) ([]sqlcdb.NewsEvent, error) {
	f.eventCalls++
	f.params = params
	return f.events, f.eventErr
}

func (f *recentNewsQueryFake) ListNewsEventEvidence(_ context.Context, ids []int64) ([]sqlcdb.ListNewsEventEvidenceRow, error) {
	f.evidenceCalls++
	f.evidenceIDs = ids
	return f.evidence, f.evidenceErr
}

func recentNewsTestServer(q recentNewsQueries) *server.MCPServer {
	srv := server.NewMCPServer("test", serverVersion)
	registerRecentNewsTools(srv, q)
	return srv
}

func callRecentNews(t *testing.T, q recentNewsQueries, args map[string]any) string {
	t.Helper()
	tool := recentNewsTestServer(q).GetTool(recentNewsToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, args)
	text := resultText(t, result)
	require.False(t, result.IsError, text)
	return text
}

func TestRecentNewsDefaults(t *testing.T) {
	q := &recentNewsQueryFake{}
	text := callRecentNews(t, q, map[string]any{recentNewsSinceArg: recentNewsTestSince})

	require.Equal(t, 1, q.eventCalls)
	assert.Equal(t, defaultRecentNewsEventsLimit, int(q.params.Limit))
	assert.Equal(t, recentNewsTestSince, q.params.LastReportedAt.Time.Format(time.RFC3339))
	assert.Zero(t, q.evidenceCalls, "an empty event list needs no evidence query")
	assert.Equal(t, noResultsText, text)
}

func TestRecentNewsFiltersBeforeLimit(t *testing.T) {
	q := &recentNewsQueryFake{events: []sqlcdb.NewsEvent{
		recentNewsEvent(1, string(newsevent.TypeTrade)),
		recentNewsEvent(2, string(newsevent.TypeInjury)),
		recentNewsEvent(3, string(newsevent.TypeInjury)),
	}}
	text := callRecentNews(t, q, map[string]any{
		recentNewsSinceArg: recentNewsTestSince,
		recentNewsTypesArg: []any{" Injury ", "injury"},
		"limit":            float64(1),
	})

	assert.Equal(t, int32(maxRecentNewsEventsScan), q.params.Limit)
	assert.Equal(t, []int64{2}, q.evidenceIDs)
	rows := parseCSV(t, text)
	require.Len(t, rows, 2)
	assert.Equal(t, recentNewsColumns, rows[0])
	assert.Equal(t, "2", rows[1][0])
}

func TestRecentNewsRendersLatestBoundedEvidence(t *testing.T) {
	quote := strings.Repeat("é", maxRecentNewsQuoteRunes+5)
	q := &recentNewsQueryFake{
		events: []sqlcdb.NewsEvent{recentNewsEvent(7, string(newsevent.TypeSuspension))},
		evidence: []sqlcdb.ListNewsEventEvidenceRow{
			recentNewsEvidenceRow(t, 7, "supports", "First source", "old quote"),
			recentNewsEvidenceRow(t, 7, "resolves", "Latest source", quote),
		},
	}
	rows := parseCSV(t, callRecentNews(t, q, map[string]any{recentNewsSinceArg: recentNewsTestSince}))
	require.Len(t, rows, 2)

	publisher := columnIndex(t, rows[0], "evidence_publisher")
	relation := columnIndex(t, rows[0], "evidence_relation")
	quoteColumn := columnIndex(t, rows[0], "evidence_quote")
	assert.Equal(t, "Latest source", rows[1][publisher])
	assert.Equal(t, "resolves", rows[1][relation])
	assert.Len(t, []rune(rows[1][quoteColumn]), maxRecentNewsQuoteRunes)
	assert.True(t, strings.HasSuffix(rows[1][quoteColumn], "…"))
}

func TestRecentNewsLeavesMissingEvidenceBlank(t *testing.T) {
	q := &recentNewsQueryFake{events: []sqlcdb.NewsEvent{recentNewsEvent(1, string(newsevent.TypeTrade))}}
	rows := parseCSV(t, callRecentNews(t, q, map[string]any{recentNewsSinceArg: recentNewsTestSince}))
	require.Len(t, rows, 2)
	assert.Empty(t, rows[1][columnIndex(t, rows[0], "evidence_publisher")])
	assert.Empty(t, rows[1][columnIndex(t, rows[0], "evidence_quote")])
}

func TestRecentNewsRejectsBadArgumentsBeforeSQL(t *testing.T) {
	tests := map[string]struct {
		args map[string]any
		want string
	}{
		"missing since":     {map[string]any{}, "since is required"},
		"date without time": {map[string]any{recentNewsSinceArg: "2026-10-16"}, "want an RFC3339 timestamp"},
		"numeric since":     {map[string]any{recentNewsSinceArg: float64(1)}, "invalid since 1"},
		"types not array":   {map[string]any{recentNewsSinceArg: recentNewsTestSince, recentNewsTypesArg: "injury"}, "want an array"},
		"type not string":   {map[string]any{recentNewsSinceArg: recentNewsTestSince, recentNewsTypesArg: []any{float64(1)}}, "invalid event_types[0] 1"},
		"unknown type":      {map[string]any{recentNewsSinceArg: recentNewsTestSince, recentNewsTypesArg: []any{"waiver"}}, `invalid event_types[0] "waiver"`},
		"zero limit":        {map[string]any{recentNewsSinceArg: recentNewsTestSince, "limit": float64(0)}, "invalid limit 0"},
		"limit above max":   {map[string]any{recentNewsSinceArg: recentNewsTestSince, "limit": float64(maxRecentNewsEventsLimit + 1)}, "invalid limit"},
		"fractional limit":  {map[string]any{recentNewsSinceArg: recentNewsTestSince, "limit": 1.5}, "invalid limit 1.5"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			q := &recentNewsQueryFake{}
			tool := recentNewsTestServer(q).GetTool(recentNewsToolName)
			result := callTool(t, tool.Handler, test.args)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), test.want)
			assert.Zero(t, q.eventCalls)
		})
	}
}

func TestRecentNewsReportsQueryErrors(t *testing.T) {
	eventsErr := &recentNewsQueryFake{eventErr: errors.New("events failed")}
	result := callRecentNewsResult(t, eventsErr)
	assert.True(t, result.IsError)
	assert.Equal(t, "list recent news events: events failed", resultText(t, result))

	evidenceErr := &recentNewsQueryFake{
		events:      []sqlcdb.NewsEvent{recentNewsEvent(1, string(newsevent.TypeInjury))},
		evidenceErr: errors.New("evidence failed"),
	}
	result = callRecentNewsResult(t, evidenceErr)
	assert.True(t, result.IsError)
	assert.Equal(t, "list news event evidence: evidence failed", resultText(t, result))
}

func TestRecentNewsReportsMalformedQuotes(t *testing.T) {
	q := &recentNewsQueryFake{
		events:   []sqlcdb.NewsEvent{recentNewsEvent(4, string(newsevent.TypeInjury))},
		evidence: []sqlcdb.ListNewsEventEvidenceRow{{EventID: 4, Quotes: []byte("not-json")}},
	}
	result := callRecentNewsResult(t, q)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "decode quotes of news event 4")
}

func callRecentNewsResult(t *testing.T, q recentNewsQueries) *mcp.CallToolResult {
	t.Helper()
	tool := recentNewsTestServer(q).GetTool(recentNewsToolName)
	return callTool(t, tool.Handler, map[string]any{recentNewsSinceArg: recentNewsTestSince})
}

func recentNewsEvent(id int64, eventType string) sqlcdb.NewsEvent {
	reported := pgtype.Timestamptz{Time: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), Valid: true}
	return sqlcdb.NewsEvent{
		ID: id, NhlPlayerID: pgtype.Int8{Int64: 8478402, Valid: true}, PlayerName: "Test Player",
		EventType: eventType, ReportStatus: "confirmed", Attribution: "the club",
		DurationKind: "unknown", Lifecycle: "active", FirstReportedAt: reported, LastReportedAt: reported,
	}
}

func recentNewsEvidenceRow(t *testing.T, eventID int64, relation, publisher, quote string) sqlcdb.ListNewsEventEvidenceRow {
	t.Helper()
	encoded, err := json.Marshal([]newsevent.Quote{{Doc: "E1", Text: quote}})
	require.NoError(t, err)
	return sqlcdb.ListNewsEventEvidenceRow{
		EventID: eventID, Relation: relation, Publisher: publisher, Kind: "official",
		ReportedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, Title: "Article", Url: "https://example.test/news",
		Quotes: encoded,
	}
}
