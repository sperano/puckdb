package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	playerNewsToolName = "get_player_news"
	// Arguments of get_player_news besides the player IDs and limit.
	newsSinceArg           = "since"
	newsIncludeInactiveArg = "include_inactive"
	// defaultPlayerNewsLimit is the number of events returned without a
	// limit argument.
	defaultPlayerNewsLimit = 20
	// maxPlayerNewsLimit caps limit to bound the result size; the header's
	// events count shows the cut.
	maxPlayerNewsLimit = 100
	// maxReportsPerEvent caps the evidence rows of one event to its latest
	// reports; the event row's reports count covers them all.
	maxReportsPerEvent = 5
	// maxQuotesPerReport caps the quotes printed for one report.
	maxQuotesPerReport = 2
	// maxQuoteRunes and maxTitleRunes bound the quoted article text.
	maxQuoteRunes = 240
	maxTitleRunes = 120
	// truncationMark ends a shortened quote or title.
	truncationMark = "…"
	// newsEventsSection and newsEvidenceSection title the two CSV sections.
	newsEventsSection   = "events"
	newsEvidenceSection = "evidence"
)

// playerNewsLimitParam bounds get_player_news's limit; the Yahoo player ID
// argument is shared with get_yahoo_player_ownership (yahooPlayerIDParam).
var playerNewsLimitParam = intArg{name: "limit", min: minimumPositiveInteger, max: maxPlayerNewsLimit}

// registerPlayerNewsTools adds the news toolset's player news tools. The
// nhl toolset's league-wide news tool is registered by registerNewsTools.
func registerPlayerNewsTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(playerNewsTool(queries))
}

func playerNewsTool(q newsevent.PlayerEventQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(playerNewsToolName,
			mcp.WithDescription("Get a player's validated news events (injury, suspension, reinstatement, trade, role change) "+
				"with short attributed quotes of the articles behind them. Events restate what reports say, checked against "+
				"their quotes; they carry no fantasy impact, and needs_review=true events are not fit for automatic use. "+
				"No events is not evidence that a player is healthy or available. "+
				"Prints a '# player=... events=... returned=...' header (events counts the matches before the limit), then "+
				"an '# events' CSV section in first-reported order, oldest first (report_status: confirmed, reported or rumor; "+
				"lifecycle: active, superseded by superseded_by, retracted or resolved; reports = evidence reports on record), "+
				"and an '# evidence' CSV section: per event its latest reports (relation supports, contradicts, retracts, "+
				"resolves or withdraws; publisher; article title and URL), one row per quote. "+
				"Pass player_id, yahoo_player_id or both: an event stored under either ID matches."),
			mcp.WithNumber(playerIDArg, mcp.Description("NHL player ID (search_player in the nhl toolset finds IDs)")),
			mcp.WithNumber(yahooPlayerIDArg, mcp.Description("Yahoo player ID, as the yahoo toolset's tools return it")),
			mcp.WithString(newsSinceArg, mcp.Description("Keep events last reported on or after this date (YYYY-MM-DD, UTC)")),
			mcp.WithNumber(playerNewsLimitParam.name, mcp.Description(fmt.Sprintf(
				"Keep the latest events by first report (default %d, max %d)", defaultPlayerNewsLimit, maxPlayerNewsLimit))),
			mcp.WithBoolean(newsIncludeInactiveArg, mcp.Description(
				"Also return superseded, retracted and resolved events (default false: active events only)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			request, errResult := playerNewsRequest(req)
			if errResult != nil {
				return errResult, nil
			}
			selected, err := newsevent.LoadPlayerEvents(ctx, q, request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := playerNewsText(request, selected)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		},
	}
}

// playerNewsRequest reads the arguments of get_player_news.
func playerNewsRequest(req mcp.CallToolRequest) (newsevent.PlayerEventsRequest, *mcp.CallToolResult) {
	var request newsevent.PlayerEventsRequest
	nhlID, hasNHLID, errResult := optionalID(req, playerIDArg)
	if errResult != nil {
		return request, errResult
	}
	yahooID, hasYahooID, errResult := optionalFilter[int32](req, yahooPlayerIDParam)
	if errResult != nil {
		return request, errResult
	}
	if !hasNHLID && !hasYahooID {
		return request, mcp.NewToolResultError(fmt.Sprintf("%s or %s is required", playerIDArg, yahooPlayerIDArg))
	}
	since, errResult := optionalDate(req, newsSinceArg)
	if errResult != nil {
		return request, errResult
	}
	limit, errResult := boundedLimitOrDefault(req, playerNewsLimitParam, defaultPlayerNewsLimit)
	if errResult != nil {
		return request, errResult
	}
	includeInactive, errResult := optionalBool(req, newsIncludeInactiveArg)
	if errResult != nil {
		return request, errResult
	}
	request.Player = news.Identity{NHLPlayerID: nhlID, YahooPlayerID: int(yahooID)}
	request.Since, request.Limit, request.IncludeInactive = since.Time, int(limit), includeInactive
	return request, nil
}

// newsEventRow is one event. The requested player goes in the header; the
// IDs stay on the row because an event may be stored under only one.
type newsEventRow struct {
	EventID         int64       `json:"event_id"`
	NHLPlayerID     pgtype.Int8 `json:"nhl_player_id"`
	YahooPlayerID   pgtype.Int4 `json:"yahoo_player_id"`
	Type            string      `json:"type"`
	ReportStatus    string      `json:"report_status"`
	Attribution     string      `json:"attribution"`
	EffectiveFrom   string      `json:"effective_from"`
	Duration        string      `json:"duration"`
	ChangeField     string      `json:"change_field"`
	ChangeFrom      string      `json:"change_from"`
	ChangeTo        string      `json:"change_to"`
	Lifecycle       string      `json:"lifecycle"`
	LifecycleReason string      `json:"lifecycle_reason"`
	SupersededBy    pgtype.Int8 `json:"superseded_by"`
	NeedsReview     bool        `json:"needs_review"`
	ReviewReason    string      `json:"review_reason"`
	FirstReportedAt time.Time   `json:"first_reported_at"`
	LastReportedAt  time.Time   `json:"last_reported_at"`
	Reports         int         `json:"reports"`
}

// newsEvidenceRow is one quote of one report behind an event.
type newsEvidenceRow struct {
	EventID    int64     `json:"event_id"`
	Relation   string    `json:"relation"`
	Publisher  string    `json:"publisher"`
	Kind       string    `json:"kind"`
	ReportedAt time.Time `json:"reported_at"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Quote      string    `json:"quote"`
}

func newNewsEventRow(ev newsevent.ReportedEvent) newsEventRow {
	e := ev.Event
	// YahooPlayerID was read from an int4 column, so the cast cannot wrap.
	row := newsEventRow{
		EventID:       ev.ID,
		NHLPlayerID:   pgtype.Int8{Int64: e.Player.NHLPlayerID, Valid: e.Player.NHLPlayerID != 0},
		YahooPlayerID: pgtype.Int4{Int32: int32(e.Player.YahooPlayerID), Valid: e.Player.YahooPlayerID != 0},
		Type:          string(e.Type), ReportStatus: string(e.Status), Attribution: e.Attribution,
		Duration:    durationCell(e.Duration),
		ChangeField: string(e.Change.Field), ChangeFrom: e.Change.From, ChangeTo: e.Change.To,
		Lifecycle: string(ev.Lifecycle), LifecycleReason: ev.LifecycleReason,
		SupersededBy: pgtype.Int8{Int64: ev.SupersededBy, Valid: ev.SupersededBy != 0},
		NeedsReview:  e.NeedsReview, ReviewReason: e.ReviewReason,
		FirstReportedAt: ev.FirstReportedAt.UTC(), LastReportedAt: ev.LastReportedAt.UTC(),
		Reports: len(ev.Evidence),
	}
	if !e.EffectiveOn.IsZero() {
		row.EffectiveFrom = e.EffectiveOn.Format(time.DateOnly)
	}
	return row
}

// durationCell prints a stated length with its count or end date:
// "games:5", "days:10", "until_date:2026-11-01", or the kind alone.
func durationCell(d newsevent.Duration) string {
	switch d.Kind {
	case newsevent.DurationGames:
		return fmt.Sprintf("%s:%d", d.Kind, d.Games)
	case newsevent.DurationDays:
		return fmt.Sprintf("%s:%d", d.Kind, d.Days)
	case newsevent.DurationUntilDate:
		return fmt.Sprintf("%s:%s", d.Kind, d.Until.Format(time.DateOnly))
	case "":
		return string(newsevent.DurationUnknown)
	default:
		return string(d.Kind)
	}
}

// newsEvidenceRows prints an event's latest maxReportsPerEvent reports, up
// to maxQuotesPerReport quotes each; a report without quotes still gets a
// row.
func newsEvidenceRows(ev newsevent.ReportedEvent) []newsEvidenceRow {
	reports := ev.Evidence
	if len(reports) > maxReportsPerEvent {
		reports = reports[len(reports)-maxReportsPerEvent:]
	}
	var rows []newsEvidenceRow
	for _, r := range reports {
		base := newsEvidenceRow{
			EventID: ev.ID, Relation: string(r.Relation), Publisher: r.Publisher, Kind: string(r.Kind),
			ReportedAt: r.ReportedAt.UTC(), Title: boundedText(r.Title, maxTitleRunes), URL: r.URL,
		}
		quotes := r.Quotes[:min(len(r.Quotes), maxQuotesPerReport)]
		if len(quotes) == 0 {
			rows = append(rows, base)
		}
		for _, q := range quotes {
			row := base
			row.Quote = boundedText(q.Text, maxQuoteRunes)
			rows = append(rows, row)
		}
	}
	return rows
}

// boundedText collapses whitespace to single spaces and cuts s to at most
// maxRunes runes, ending a cut with truncationMark.
func boundedText(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxRunes-utf8.RuneCountInString(truncationMark)])) + truncationMark
}

// playerNewsText renders the header and the events and evidence sections.
func playerNewsText(request newsevent.PlayerEventsRequest, selected newsevent.PlayerEvents) (string, error) {
	events := make([]newsEventRow, 0, len(selected.Events))
	var evidence []newsEvidenceRow
	var name string
	for _, ev := range selected.Events {
		events = append(events, newNewsEventRow(ev))
		evidence = append(evidence, newsEvidenceRows(ev)...)
		name = ev.Event.Player.Name
	}
	var h metadataHeader
	h.add("player", name)
	h.add(playerIDArg, optionalHeaderID(request.Player.NHLPlayerID))
	h.add(yahooPlayerIDArg, optionalHeaderID(int64(request.Player.YahooPlayerID)))
	since := ""
	if !request.Since.IsZero() {
		since = request.Since.Format(time.DateOnly)
	}
	h.add(newsSinceArg, since)
	h.add(newsIncludeInactiveArg, request.IncludeInactive)
	h.add("events", selected.Matching)
	h.add("returned", len(events))
	eventSection, err := csvSection(newsEventsSection, events)
	if err != nil {
		return "", err
	}
	evidenceSection, err := csvSection(newsEvidenceSection, evidence)
	if err != nil {
		return "", err
	}
	return h.String() + "\n" + eventSection + evidenceSection, nil
}

// optionalHeaderID prints an ID, or "" when it was not given.
func optionalHeaderID(id int64) string {
	if id == 0 {
		return ""
	}
	return fmt.Sprint(id)
}
