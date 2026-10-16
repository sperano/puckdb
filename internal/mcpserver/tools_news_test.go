package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	newsPlayerNHLID   int64 = 8480018
	newsPlayerYahooID int32 = 6743
	newsPlayerName          = "Nick Suzuki"
	// Event IDs of the fake fixture, in first-reported order.
	newsSupersededEventID int64 = 11
	newsActiveEventID     int64 = 12
	newsRumorEventID      int64 = 13

	// PostgreSQL fixture of TestPlayerNewsToolReadsDatabase.
	pgNewsNHLPlayerID = 990753001
	pgNewsPublisher   = "MCP news test"
)

var newsBaseTime = time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)

// fakeNewsQueries serves fixed events and evidence and records the
// player each call asked for.
type fakeNewsQueries struct {
	events   []sqlcdb.NewsEvent
	evidence []sqlcdb.ListNewsEventEvidenceRow
	err      error
	params   []sqlcdb.ListNewsEventsForPlayerParams
}

func (f *fakeNewsQueries) ListNewsEventsForPlayer(_ context.Context, arg sqlcdb.ListNewsEventsForPlayerParams) ([]sqlcdb.NewsEvent, error) {
	f.params = append(f.params, arg)
	return f.events, f.err
}

func (f *fakeNewsQueries) ListNewsEventEvidence(_ context.Context, eventIDs []int64) ([]sqlcdb.ListNewsEventEvidenceRow, error) {
	var rows []sqlcdb.ListNewsEventEvidenceRow
	for _, row := range f.evidence {
		if slices.Contains(eventIDs, row.EventID) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (f *fakeNewsQueries) ListNewsEventTransitions(context.Context, []int64) ([]sqlcdb.NewsEventTransition, error) {
	return nil, nil
}

// newFakeNewsQueries holds a suspension superseded by a longer one, and an
// active trade rumor flagged for review.
func newFakeNewsQueries(t *testing.T) *fakeNewsQueries {
	f := &fakeNewsQueries{events: []sqlcdb.NewsEvent{
		newsEvent(newsSupersededEventID, "suspension", "reported", 0, 0),
		newsEvent(newsActiveEventID, "suspension", "confirmed", 1, 2),
		newsEvent(newsRumorEventID, "trade", "rumor", 3, 3),
	}}
	f.events[0].DurationKind, f.events[0].DurationGames = "games", pgtype.Int4{Int32: 2, Valid: true}
	f.events[0].Lifecycle, f.events[0].LifecycleReason = "superseded", "updated by NHL.com version 2"
	f.events[0].SupersededBy = pgtype.Int8{Int64: newsActiveEventID, Valid: true}
	f.events[1].DurationKind, f.events[1].DurationGames = "games", pgtype.Int4{Int32: 4, Valid: true}
	f.events[1].Attribution = "NHL Department of Player Safety"
	f.events[1].EffectiveFrom = pgtype.Date{Time: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), Valid: true}
	f.events[2].ChangeField, f.events[2].ChangeTo = "team", "Boston Bruins"
	f.events[2].NeedsReview, f.events[2].ReviewReason = true, "single unnamed source"
	f.evidence = []sqlcdb.ListNewsEventEvidenceRow{
		newsEvidence(t, newsActiveEventID, "supports", "NHL.com", 1, "Suzuki suspended four games",
			"Nick Suzuki has been suspended for four games."),
		newsEvidence(t, newsRumorEventID, "supports", "Local Beat", 3, "Trade winds",
			"Suzuki could be moved to Boston,", "according to one source."),
	}
	return f
}

// newsEvent builds a stored event of the fixture player, first and last
// reported the given number of days after newsBaseTime.
func newsEvent(id int64, eventType, status string, firstDay, lastDay int) sqlcdb.NewsEvent {
	return sqlcdb.NewsEvent{
		ID: id, NhlPlayerID: pgtype.Int8{Int64: newsPlayerNHLID, Valid: true},
		YahooPlayerID: pgtype.Int4{Int32: newsPlayerYahooID, Valid: true}, PlayerName: newsPlayerName,
		EventType: eventType, ReportStatus: status, DurationKind: "unknown", Lifecycle: "active",
		FirstReportedAt: news.Timestamptz(newsBaseTime.AddDate(0, 0, firstDay)),
		LastReportedAt:  news.Timestamptz(newsBaseTime.AddDate(0, 0, lastDay)),
	}
}

func newsEvidence(t *testing.T, eventID int64, relation, publisher string, day int, title string, quotes ...string) sqlcdb.ListNewsEventEvidenceRow {
	t.Helper()
	stored := make([]newsevent.Quote, len(quotes))
	for i, q := range quotes {
		stored[i] = newsevent.Quote{Doc: "E1", Text: q}
	}
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	return sqlcdb.ListNewsEventEvidenceRow{
		EventID: eventID, Relation: relation, Publisher: publisher, Kind: string(news.KindReporting),
		ReportedAt: news.Timestamptz(newsBaseTime.AddDate(0, 0, day)), Quotes: raw, ExtractionID: eventID * 10,
		VersionID: eventID * 100, Version: 1, Title: title, Url: "https://example.com/" + fmt.Sprint(eventID),
	}
}

func callPlayerNews(t *testing.T, q newsevent.PlayerEventQueries, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	return callTool(t, playerNewsTool(q).Handler, args)
}

// newsOutput splits a get_player_news result into its header and the
// events and evidence CSV rows (header row included).
func newsOutput(t *testing.T, result *mcp.CallToolResult) (string, [][]string, [][]string) {
	t.Helper()
	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)
	header, rest, ok := strings.Cut(text, "\n")
	require.True(t, ok, text)
	eventsPart, evidencePart, ok := strings.Cut(rest, commentPrefix+newsEvidenceSection+"\n")
	require.True(t, ok, text)
	eventsCSV, ok := strings.CutPrefix(eventsPart, commentPrefix+newsEventsSection+"\n")
	require.True(t, ok, text)
	return header, sectionRows(t, eventsCSV), sectionRows(t, evidencePart)
}

func sectionRows(t *testing.T, text string) [][]string {
	t.Helper()
	if strings.TrimSpace(text) == noResultsText {
		return nil
	}
	return parseCSV(t, text)
}

func rowValues(t *testing.T, rows [][]string, column string) []string {
	t.Helper()
	col := columnIndex(t, rows[0], column)
	values := make([]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		values = append(values, row[col])
	}
	return values
}

func TestPlayerNewsToolReturnsActiveEventsWithEvidence(t *testing.T) {
	q := newFakeNewsQueries(t)
	header, events, evidence := newsOutput(t, callPlayerNews(t, q, map[string]any{playerIDArg: float64(newsPlayerNHLID)}))

	assert.Equal(t, fmt.Sprintf(`# player="%s" player_id=%d yahoo_player_id="" since="" include_inactive=false events=2 returned=2`,
		newsPlayerName, newsPlayerNHLID), header)
	require.Len(t, events, 3)
	assert.Equal(t, []string{"12", "13"}, rowValues(t, events, "event_id"))
	assert.Equal(t, []string{"games:4", "unknown"}, rowValues(t, events, "duration"))
	assert.Equal(t, []string{"2026-10-02", ""}, rowValues(t, events, "effective_from"))
	assert.Equal(t, []string{"NHL Department of Player Safety", ""}, rowValues(t, events, "attribution"))
	assert.Equal(t, []string{"", "Boston Bruins"}, rowValues(t, events, "change_to"))
	assert.Equal(t, []string{"false", "true"}, rowValues(t, events, "needs_review"))
	assert.Equal(t, []string{"2026-10-02T15:00:00Z", "2026-10-04T15:00:00Z"}, rowValues(t, events, "first_reported_at"))
	assert.Equal(t, []string{"1", "1"}, rowValues(t, events, "reports"))

	require.Len(t, evidence, 4, "one row per quote")
	assert.Equal(t, []string{"12", "13", "13"}, rowValues(t, evidence, "event_id"))
	assert.Equal(t, []string{"Nick Suzuki has been suspended for four games.", "Suzuki could be moved to Boston,",
		"according to one source."}, rowValues(t, evidence, "quote"))
	assert.Equal(t, []string{"NHL.com", "Local Beat", "Local Beat"}, rowValues(t, evidence, "publisher"))

	require.Len(t, q.params, 1)
	assert.Equal(t, sqlcdb.ListNewsEventsForPlayerParams{NhlPlayerID: pgtype.Int8{Int64: newsPlayerNHLID, Valid: true}}, q.params[0])
}

func TestPlayerNewsToolIncludeInactive(t *testing.T) {
	_, events, _ := newsOutput(t, callPlayerNews(t, newFakeNewsQueries(t),
		map[string]any{playerIDArg: float64(newsPlayerNHLID), newsIncludeInactiveArg: true}))

	assert.Equal(t, []string{"11", "12", "13"}, rowValues(t, events, "event_id"))
	assert.Equal(t, []string{"superseded", "active", "active"}, rowValues(t, events, "lifecycle"))
	assert.Equal(t, []string{"12", "", ""}, rowValues(t, events, "superseded_by"))
	assert.Equal(t, []string{"updated by NHL.com version 2", "", ""}, rowValues(t, events, "lifecycle_reason"))
	assert.Equal(t, []string{"0", "1", "1"}, rowValues(t, events, "reports"))
}

func TestPlayerNewsToolSinceAndLimit(t *testing.T) {
	header, events, evidence := newsOutput(t, callPlayerNews(t, newFakeNewsQueries(t), map[string]any{
		yahooPlayerIDArg: float64(newsPlayerYahooID), newsSinceArg: "2026-10-03", playerNewsLimitParam.name: float64(1),
		newsIncludeInactiveArg: "true",
	}))

	assert.Equal(t, fmt.Sprintf(`# player="%s" player_id="" yahoo_player_id=%d since=2026-10-03 include_inactive=true events=2 returned=1`,
		newsPlayerName, newsPlayerYahooID), header, "events counts the matches before the limit")
	assert.Equal(t, []string{"13"}, rowValues(t, events, "event_id"), "the limit keeps the latest event")
	assert.Equal(t, []string{"13", "13"}, rowValues(t, evidence, "event_id"))
}

func TestPlayerNewsToolPassesBothIDs(t *testing.T) {
	q := newFakeNewsQueries(t)
	callPlayerNews(t, q, map[string]any{playerIDArg: float64(newsPlayerNHLID), yahooPlayerIDArg: float64(newsPlayerYahooID)})

	require.Len(t, q.params, 1)
	assert.Equal(t, sqlcdb.ListNewsEventsForPlayerParams{
		NhlPlayerID:   pgtype.Int8{Int64: newsPlayerNHLID, Valid: true},
		YahooPlayerID: pgtype.Int4{Int32: newsPlayerYahooID, Valid: true},
	}, q.params[0])
}

func TestPlayerNewsToolNoEvents(t *testing.T) {
	header, events, evidence := newsOutput(t, callPlayerNews(t, &fakeNewsQueries{}, map[string]any{playerIDArg: float64(newsPlayerNHLID)}))

	assert.Contains(t, header, `player="" `)
	assert.Contains(t, header, "events=0 returned=0")
	assert.Nil(t, events)
	assert.Nil(t, evidence)
}

func TestPlayerNewsToolQueryErrorIsToolError(t *testing.T) {
	result := callPlayerNews(t, &fakeNewsQueries{err: errors.New("connection refused")}, map[string]any{playerIDArg: float64(newsPlayerNHLID)})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "connection refused")
}

func TestPlayerNewsToolRejectsInvalidArgumentsBeforeSQL(t *testing.T) {
	valid := map[string]any{playerIDArg: float64(newsPlayerNHLID)}
	tests := map[string]struct {
		args map[string]any
		want string
	}{
		"no player":                {map[string]any{}, "player_id or yahoo_player_id is required"},
		"fractional player_id":     {map[string]any{playerIDArg: 8480018.5}, "invalid player_id"},
		"negative yahoo id":        {map[string]any{yahooPlayerIDArg: float64(-1)}, "invalid yahoo_player_id"},
		"yahoo id beyond int4":     {map[string]any{yahooPlayerIDArg: float64(math.MaxInt32 + 1)}, "invalid yahoo_player_id"},
		"malformed since":          {withArg(valid, newsSinceArg, "2026-02-30"), "invalid since"},
		"zero limit":               {withArg(valid, playerNewsLimitParam.name, float64(0)), "invalid limit"},
		"limit beyond maximum":     {withArg(valid, playerNewsLimitParam.name, float64(maxPlayerNewsLimit+1)), "invalid limit"},
		"wrong include_inactive":   {withArg(valid, newsIncludeInactiveArg, float64(1)), "invalid include_inactive"},
		"non-numeric yahoo string": {map[string]any{yahooPlayerIDArg: "abc"}, "invalid yahoo_player_id"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			q := newFakeNewsQueries(t)
			result := callPlayerNews(t, q, tt.args)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), tt.want)
			assert.Empty(t, q.params, "no statement runs")
		})
	}
}

// TestPlayerNewsToolBoundsEvidence keeps a much-repeated event compact: the
// latest reports only, a few quotes each, each quote cut short.
func TestPlayerNewsToolBoundsEvidence(t *testing.T) {
	q := &fakeNewsQueries{events: []sqlcdb.NewsEvent{newsEvent(newsActiveEventID, "injury", "confirmed", 0, 0)}}
	longQuote := strings.Repeat("word ", maxQuoteRunes)
	const reports = maxReportsPerEvent + 2
	for day := range reports {
		q.evidence = append(q.evidence, newsEvidence(t, newsActiveEventID, "supports", fmt.Sprintf("Publisher %d", day), day,
			"Title", longQuote, "second quote here", "third quote dropped"))
	}
	q.evidence = append(q.evidence, newsEvidence(t, newsActiveEventID, "withdraws", "Last Word", reports, "Corrected"))

	_, events, evidence := newsOutput(t, callPlayerNews(t, q, map[string]any{playerIDArg: float64(newsPlayerNHLID)}))

	assert.Equal(t, []string{fmt.Sprint(reports + 1)}, rowValues(t, events, "reports"), "reports counts every report")
	publishers := slices.Compact(rowValues(t, evidence, "publisher"))
	assert.Equal(t, []string{"Publisher 3", "Publisher 4", "Publisher 5", "Publisher 6", "Last Word"}, publishers)
	quotes := rowValues(t, evidence, "quote")
	require.Len(t, quotes, (maxReportsPerEvent-1)*maxQuotesPerReport+1, "a report without quotes keeps one row")
	assert.NotContains(t, quotes, "third quote dropped")
	assert.Equal(t, "", quotes[len(quotes)-1])
	assert.Equal(t, maxQuoteRunes, utf8.RuneCountInString(quotes[0]))
	assert.True(t, strings.HasSuffix(quotes[0], truncationMark))
}

func TestBoundedText(t *testing.T) {
	assert.Equal(t, "placed on injured reserve", boundedText("  placed on\n injured\treserve ", maxQuoteRunes))
	assert.Equal(t, "Montréal", boundedText("Montréal", len("Montréal")-1), "runes, not bytes")
	assert.Equal(t, "abc"+truncationMark, boundedText("abc defgh", 4+utf8.RuneCountInString(truncationMark)))
}

func TestDurationCell(t *testing.T) {
	until := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	tests := map[string]newsevent.Duration{
		"games:5":               {Kind: newsevent.DurationGames, Games: 5},
		"days:10":               {Kind: newsevent.DurationDays, Days: 10},
		"until_date:2026-11-01": {Kind: newsevent.DurationUntilDate, Until: until},
		"day_to_day":            {Kind: newsevent.DurationDayToDay},
		"unknown":               {},
	}
	for want, d := range tests {
		assert.Equal(t, want, durationCell(d))
	}
}

// TestPlayerNewsToolReadsDatabase runs the tool over the real queries: one
// event with one quoted report, stored under the NHL ID only.
func TestPlayerNewsToolReadsDatabase(t *testing.T) {
	pool := openMCPTestDB(t)
	cleanup := func() {
		ctx := context.Background()
		_, err := pool.Exec(ctx, `DELETE FROM news_events WHERE nhl_player_id = $1`, pgNewsNHLPlayerID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `DELETE FROM news_articles WHERE publisher = $1`, pgNewsPublisher)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	seedNewsEvent(t, pool)

	srv := server.NewMCPServer("test", serverVersion)
	registerNewsTools(srv, sqlcdb.New(pool))
	tool := srv.GetTool(playerNewsToolName)
	require.NotNil(t, tool)
	header, events, evidence := newsOutput(t, callTool(t, tool.Handler, map[string]any{playerIDArg: float64(pgNewsNHLPlayerID)}))

	assert.Equal(t, fmt.Sprintf(`# player="Test Skater" player_id=%d yahoo_player_id="" since="" include_inactive=false events=1 returned=1`,
		pgNewsNHLPlayerID), header)
	assert.Equal(t, []string{"injury"}, rowValues(t, events, "type"))
	assert.Equal(t, []string{"days:10"}, rowValues(t, events, "duration"))
	assert.Equal(t, []string{""}, rowValues(t, events, "yahoo_player_id"))
	assert.Equal(t, []string{"Skater out ten days"}, rowValues(t, evidence, "title"))
	assert.Equal(t, []string{"is expected to miss ten days"}, rowValues(t, evidence, "quote"))
	assert.Equal(t, []string{"2026-10-05T12:00:00Z"}, rowValues(t, evidence, "reported_at"))
}

// seedNewsEvent stores an article version, its extraction, one event and
// the event's evidence.
func seedNewsEvent(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	reported := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	var articleID, versionID, extractionID, eventID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO news_articles (publisher, external_id, source_id, kind, url, first_seen_at, last_seen_at)
		VALUES ($1, 'mcp-1', 'mcp-test', 'reporting', 'https://example.com/a', $2, $2) RETURNING id`,
		pgNewsPublisher, reported).Scan(&articleID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO news_article_versions (article_id, version, content_hash, title_fingerprint,
		text_fingerprint, title, evidence_text, url, retrieved_at)
		VALUES ($1, 1, 'h', 't', 'x', 'Skater out ten days', 'text', 'https://example.com/a', $2) RETURNING id`,
		articleID, reported).Scan(&versionID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO news_extractions (version_id, extractor_key, provider, model, prompt_version,
		schema_version, input_hash, status) VALUES ($1, 'mcp/test', 'mcp', 'test', 'p1', 's1', 'i', 'succeeded') RETURNING id`,
		versionID).Scan(&extractionID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO news_events (nhl_player_id, player_name, event_type, report_status,
		duration_kind, duration_days, first_reported_at, last_reported_at, extraction_id)
		VALUES ($1, 'Test Skater', 'injury', 'confirmed', 'days', 10, $2, $2, $3) RETURNING id`,
		pgNewsNHLPlayerID, reported, extractionID).Scan(&eventID))
	_, err := pool.Exec(ctx, `INSERT INTO news_event_evidence (event_id, version_id, extraction_id, relation, article_id,
		publisher, kind, reported_at, quotes) VALUES ($1, $2, $3, 'supports', $4, $5, 'reporting', $6, $7)`,
		eventID, versionID, extractionID, articleID, pgNewsPublisher, reported,
		`[{"doc": "E1", "quote": "is expected to miss ten days"}]`)
	require.NoError(t, err)
}
