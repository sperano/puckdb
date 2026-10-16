package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	recentNewsToolName = "get_recent_news_events"
	recentNewsSinceArg = "since"
	recentNewsTypesArg = "event_types"

	// defaultRecentNewsEventsLimit covers a busy day without crowding the
	// model context.
	defaultRecentNewsEventsLimit = 25
	// maxRecentNewsEventsLimit keeps the returned CSV bounded.
	maxRecentNewsEventsLimit = 100
	// maxRecentNewsEventsScan leaves room for an event-type filter while the
	// existing query can filter only by time. The returned rows remain capped
	// by maxRecentNewsEventsLimit.
	maxRecentNewsEventsScan = 1000
	// maxRecentNewsQuoteRunes keeps each evidence excerpt short while avoiding
	// splitting a multi-byte character.
	maxRecentNewsQuoteRunes = 240
)

var (
	recentNewsLimitParam = intArg{name: "limit", min: minimumPositiveInteger, max: maxRecentNewsEventsLimit}
	recentNewsTypes      = []string{
		string(newsevent.TypeInjury),
		string(newsevent.TypeSuspension),
		string(newsevent.TypeReinstatement),
		string(newsevent.TypeTrade),
		string(newsevent.TypeRoleChange),
	}
)

type recentNewsQueries interface {
	ListRecentNewsEvents(context.Context, sqlcdb.ListRecentNewsEventsParams) ([]sqlcdb.NewsEvent, error)
	ListNewsEventEvidence(context.Context, []int64) ([]sqlcdb.ListNewsEventEvidenceRow, error)
}

type recentNewsRequest struct {
	since      time.Time
	eventTypes []string
	limit      int32
}

// recentNewsRow is one validated event with its latest attributed evidence.
type recentNewsRow struct {
	EventID            int64              `json:"event_id"`
	NHLPlayerID        pgtype.Int8        `json:"nhl_player_id"`
	YahooPlayerID      pgtype.Int4        `json:"yahoo_player_id"`
	PlayerName         string             `json:"player_name"`
	EventType          string             `json:"event_type"`
	ReportStatus       string             `json:"report_status"`
	Attribution        string             `json:"attribution"`
	EffectiveFrom      pgtype.Date        `json:"effective_from"`
	DurationKind       string             `json:"duration_kind"`
	DurationGames      pgtype.Int4        `json:"duration_games"`
	DurationDays       pgtype.Int4        `json:"duration_days"`
	DurationUntil      pgtype.Date        `json:"duration_until"`
	ChangeField        string             `json:"change_field"`
	ChangeFrom         string             `json:"change_from"`
	ChangeTo           string             `json:"change_to"`
	Lifecycle          string             `json:"lifecycle"`
	SupersededBy       pgtype.Int8        `json:"superseded_by"`
	LifecycleReason    string             `json:"lifecycle_reason"`
	FirstReportedAt    pgtype.Timestamptz `json:"first_reported_at"`
	LastReportedAt     pgtype.Timestamptz `json:"last_reported_at"`
	NeedsReview        bool               `json:"needs_review"`
	ReviewReason       string             `json:"review_reason"`
	EvidenceRelation   string             `json:"evidence_relation"`
	EvidencePublisher  string             `json:"evidence_publisher"`
	EvidenceKind       string             `json:"evidence_kind"`
	EvidenceReportedAt pgtype.Timestamptz `json:"evidence_reported_at"`
	EvidenceTitle      string             `json:"evidence_title"`
	EvidenceURL        string             `json:"evidence_url"`
	EvidenceQuote      string             `json:"evidence_quote"`
}

type recentNewsEvidence struct {
	relation   string
	publisher  string
	kind       string
	reportedAt pgtype.Timestamptz
	title      string
	url        string
	quote      string
}

// registerNewsTools adds public, league-wide news tools to the NHL toolset.
func registerNewsTools(srv *server.MCPServer, queries recentNewsQueries) {
	srv.AddTools(recentNewsTool(queries))
}

func recentNewsTool(q recentNewsQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(recentNewsToolName,
			mcp.WithDescription(fmt.Sprintf("Get validated player-news events reported across the NHL since an RFC3339 time, newest first. "+
				"Each CSV row includes the latest stored evidence source and one bounded quote; blank evidence fields mean no evidence row is stored. "+
				"With event_types, the filter searches the newest %d events in the time window.", maxRecentNewsEventsScan)),
			mcp.WithString(recentNewsSinceArg, mcp.Required(), mcp.Description("Earliest report time, inclusive (RFC3339, e.g. 2026-10-16T00:00:00Z)")),
			mcp.WithArray(recentNewsTypesArg,
				mcp.Description("Optional event types; omit or pass empty for all types"),
				mcp.WithStringEnumItems(recentNewsTypes),
			),
			mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max events (default %d, max %d)", defaultRecentNewsEventsLimit, maxRecentNewsEventsLimit))),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			parsed, errResult := parseRecentNewsRequest(req)
			if errResult != nil {
				return errResult, nil
			}
			rows, err := loadRecentNewsRows(ctx, q, parsed)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(rows)
		},
	}
}

func parseRecentNewsRequest(req mcp.CallToolRequest) (recentNewsRequest, *mcp.CallToolResult) {
	var parsed recentNewsRequest
	var errResult *mcp.CallToolResult
	if parsed.since, errResult = requireRFC3339(req, recentNewsSinceArg); errResult != nil {
		return parsed, errResult
	}
	if parsed.eventTypes, errResult = optionalNewsEventTypes(req); errResult != nil {
		return parsed, errResult
	}
	parsed.limit, errResult = boundedLimitOrDefault(req, recentNewsLimitParam, defaultRecentNewsEventsLimit)
	return parsed, errResult
}

func requireRFC3339(req mcp.CallToolRequest, name string) (time.Time, *mcp.CallToolResult) {
	raw, present := argument(req, name)
	if !present {
		return time.Time{}, mcp.NewToolResultError(name + " is required")
	}
	text, ok := raw.(string)
	if !ok {
		return time.Time{}, invalidRFC3339(name, raw)
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, invalidRFC3339(name, raw)
	}
	return parsed, nil
}

func invalidRFC3339(name string, raw any) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: want an RFC3339 timestamp", name, formatRaw(raw)))
}

func optionalNewsEventTypes(req mcp.CallToolRequest) ([]string, *mcp.CallToolResult) {
	raw, present := argument(req, recentNewsTypesArg)
	if !present {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: want an array of event types", recentNewsTypesArg, formatRaw(raw)))
	}
	values := make([]string, 0, len(items))
	for i, item := range items {
		value, isString := item.(string)
		value = strings.ToLower(strings.TrimSpace(value))
		if !isString || !slices.Contains(recentNewsTypes, value) {
			return nil, invalidNewsEventType(i, item)
		}
		if !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	return values, nil
}

func invalidNewsEventType(index int, raw any) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("invalid %s[%d] %s: want any of %s",
		recentNewsTypesArg, index, formatRaw(raw), strings.Join(recentNewsTypes, ", ")))
}

func loadRecentNewsRows(ctx context.Context, q recentNewsQueries, req recentNewsRequest) ([]recentNewsRow, error) {
	queryLimit := req.limit
	if len(req.eventTypes) > 0 {
		queryLimit = maxRecentNewsEventsScan
	}
	events, err := q.ListRecentNewsEvents(ctx, sqlcdb.ListRecentNewsEventsParams{
		LastReportedAt: pgtype.Timestamptz{Time: req.since, Valid: true},
		Limit:          queryLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("list recent news events: %w", err)
	}
	events = selectRecentNewsEvents(events, req.eventTypes, int(req.limit))
	return addRecentNewsEvidence(ctx, q, events)
}

func selectRecentNewsEvents(events []sqlcdb.NewsEvent, types []string, limit int) []sqlcdb.NewsEvent {
	selected := make([]sqlcdb.NewsEvent, 0, min(len(events), limit))
	for _, event := range events {
		if len(types) > 0 && !slices.Contains(types, event.EventType) {
			continue
		}
		selected = append(selected, event)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func addRecentNewsEvidence(ctx context.Context, q recentNewsQueries, events []sqlcdb.NewsEvent) ([]recentNewsRow, error) {
	if len(events) == 0 {
		return []recentNewsRow{}, nil
	}
	ids := make([]int64, len(events))
	for i, event := range events {
		ids[i] = event.ID
	}
	evidenceRows, err := q.ListNewsEventEvidence(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list news event evidence: %w", err)
	}
	evidence, err := latestRecentNewsEvidence(evidenceRows)
	if err != nil {
		return nil, err
	}
	rows := make([]recentNewsRow, len(events))
	for i, event := range events {
		rows[i] = newRecentNewsRow(event, evidence[event.ID])
	}
	return rows, nil
}

func latestRecentNewsEvidence(rows []sqlcdb.ListNewsEventEvidenceRow) (map[int64]recentNewsEvidence, error) {
	latest := make(map[int64]recentNewsEvidence, len(rows))
	for _, row := range rows {
		var quotes []newsevent.Quote
		if err := json.Unmarshal(row.Quotes, &quotes); err != nil {
			return nil, fmt.Errorf("decode quotes of news event %d: %w", row.EventID, err)
		}
		var quote string
		if len(quotes) > 0 {
			quote = boundRecentNewsQuote(quotes[0].Text)
		}
		latest[row.EventID] = recentNewsEvidence{
			relation: row.Relation, publisher: row.Publisher, kind: row.Kind,
			reportedAt: row.ReportedAt, title: row.Title, url: row.Url, quote: quote,
		}
	}
	return latest, nil
}

func boundRecentNewsQuote(quote string) string {
	runes := []rune(quote)
	if len(runes) <= maxRecentNewsQuoteRunes {
		return quote
	}
	return string(runes[:maxRecentNewsQuoteRunes-1]) + "…"
}

func newRecentNewsRow(event sqlcdb.NewsEvent, evidence recentNewsEvidence) recentNewsRow {
	return recentNewsRow{
		EventID: event.ID, NHLPlayerID: event.NhlPlayerID, YahooPlayerID: event.YahooPlayerID,
		PlayerName: event.PlayerName, EventType: event.EventType, ReportStatus: event.ReportStatus,
		Attribution: event.Attribution, EffectiveFrom: event.EffectiveFrom, DurationKind: event.DurationKind,
		DurationGames: event.DurationGames, DurationDays: event.DurationDays, DurationUntil: event.DurationUntil,
		ChangeField: event.ChangeField, ChangeFrom: event.ChangeFrom, ChangeTo: event.ChangeTo,
		Lifecycle: event.Lifecycle, SupersededBy: event.SupersededBy, LifecycleReason: event.LifecycleReason,
		FirstReportedAt: event.FirstReportedAt, LastReportedAt: event.LastReportedAt,
		NeedsReview: event.NeedsReview, ReviewReason: event.ReviewReason,
		EvidenceRelation: evidence.relation, EvidencePublisher: evidence.publisher, EvidenceKind: evidence.kind,
		EvidenceReportedAt: evidence.reportedAt, EvidenceTitle: evidence.title, EvidenceURL: evidence.url,
		EvidenceQuote: evidence.quote,
	}
}
