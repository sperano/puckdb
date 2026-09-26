package newsadjust

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/projection"
)

// extractedIDPrefix marks adjustment event IDs taken from stored extraction
// events (internal/newsevent).
const extractedIDPrefix = "news-event:"

// holdSeparator joins the reasons an event is held.
const holdSeparator = "; "

// yahooPlayerKeyMarker separates a Yahoo player key's game key from the
// player ID ("465.p.1234").
const yahooPlayerKeyMarker = ".p."

// ExtractedEvent is one event stored by news event extraction, with every
// evidence row and lifecycle transition on record.
type ExtractedEvent struct {
	ID           int64
	Event        newsevent.Event
	IncidentID   int64
	ExtractorKey string
	Evidence     []ExtractedEvidence
	Transitions  []ExtractedTransition
}

// ExtractedEvidence is one article version linked to an event by one
// extraction, and when the link was recorded.
type ExtractedEvidence struct {
	VersionID    int64
	ExtractionID int64
	Relation     newsevent.Relation
	Publisher    string
	Kind         news.Kind
	URL          string
	ReportedAt   time.Time
	RetrievedAt  time.Time
	AddedAt      time.Time
	Quotes       []newsevent.Quote
}

// ExtractedTransition is one recorded lifecycle change of an event.
type ExtractedTransition struct {
	To           newsevent.Lifecycle
	VersionID    int64
	ExtractionID int64
	At           time.Time
}

// Extraction is what converting stored extraction events reads besides the
// events: the baseline players (to find each event's projection key), the
// NHL teams (to resolve a trade destination) and the extractors whose
// latest evaluation passed every release threshold.
type Extraction struct {
	Players  []projection.PlayerProjection
	Teams    TeamDirectory
	Released map[string]bool
}

// ConvertExtracted turns stored extraction events into adjustment event
// versions: one version per recorded change (a report reconciled into the
// event, or a lifecycle transition), each recorded when that change was, so
// Apply at a historical as-of time sees what extraction knew then. Events
// routed to review, created by an extractor that has not passed evaluation,
// or without a numeric mapping are held: they only alert. Versions that
// cannot form a valid event are dropped with a warning.
func ConvertExtracted(events []ExtractedEvent, x Extraction) ([]Event, []string) {
	c := newConverter(events, x)
	var out []Event
	var warnings []string
	for _, e := range events {
		versions, warning := c.versions(e)
		out = append(out, versions...)
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return out, warnings
}

// converter holds the indexes that link events to each other and to the
// baseline players.
type converter struct {
	extraction    Extraction
	byNHLID       map[int64]string
	byYahooID     map[int]string
	reinstatement map[reportKey]reinstatementReport
	resolvedAt    map[reportKey][]int64
	assignments   map[string][]*ExtractedEvent
}

// reportKey is what one report (extraction and article version) said
// about one player.
type reportKey struct {
	step   stepKey
	player string
}

// reinstatementReport is a reinstatement and when one report stated it.
type reinstatementReport struct {
	event      *ExtractedEvent
	reportedAt time.Time
}

func newConverter(events []ExtractedEvent, x Extraction) *converter {
	c := &converter{
		extraction: x, byNHLID: make(map[int64]string), byYahooID: make(map[int]string),
		reinstatement: make(map[reportKey]reinstatementReport), resolvedAt: make(map[reportKey][]int64),
		assignments: make(map[string][]*ExtractedEvent),
	}
	for _, p := range x.Players {
		if p.PlayerID != nil {
			c.byNHLID[*p.PlayerID] = p.PlayerKey
		}
		if id, ok := yahooPlayerID(p.PlayerKey); ok {
			c.byYahooID[id] = p.PlayerKey
		}
	}
	for i := range events {
		e := &events[i]
		player := c.playerKey(e.Event.Player)
		c.indexResolutions(e, player)
		if e.Event.Change.Field == newsevent.ChangeLeague && leagueMove(e.Event.Change.To) == moveAssignment {
			c.assignments[player] = append(c.assignments[player], e)
		}
	}
	return c
}

// indexResolutions records the reports that state a reinstatement and the
// reports that resolved an injury or suspension: by a resolves link, or by
// a transition, which is all there is when one report states both the
// absence and its end.
func (c *converter) indexResolutions(e *ExtractedEvent, player string) {
	for _, ev := range e.Evidence {
		key, ok := playerReport(ev.ExtractionID, ev.VersionID, player)
		switch {
		case !ok:
		case ev.Relation == newsevent.RelationResolves:
			c.addResolved(key, e.ID)
		case ev.Relation == newsevent.RelationSupports && e.Event.Type == newsevent.TypeReinstatement:
			if _, exists := c.reinstatement[key]; !exists {
				c.reinstatement[key] = reinstatementReport{event: e, reportedAt: ev.ReportedAt.UTC()}
			}
		}
	}
	if e.Event.Type != newsevent.TypeInjury && e.Event.Type != newsevent.TypeSuspension {
		return
	}
	for _, t := range e.Transitions {
		if key, ok := playerReport(t.ExtractionID, t.VersionID, player); ok && t.To == newsevent.LifecycleResolved {
			c.addResolved(key, e.ID)
		}
	}
}

func (c *converter) addResolved(key reportKey, id int64) {
	if !slices.Contains(c.resolvedAt[key], id) {
		c.resolvedAt[key] = append(c.resolvedAt[key], id)
	}
}

// playerReport keys a row by its report; a row naming no report has none.
func playerReport(extraction, version int64, player string) (reportKey, bool) {
	if extraction == 0 && version == 0 {
		return reportKey{}, false
	}
	return reportKey{step: stepKey{extraction: extraction, version: version}, player: player}, true
}

// yahooPlayerID reads the player ID of a Yahoo player key.
func yahooPlayerID(key string) (int, bool) {
	i := strings.LastIndex(key, yahooPlayerKeyMarker)
	if i < 0 {
		return 0, false
	}
	id, err := strconv.Atoi(key[i+len(yahooPlayerKeyMarker):])
	return id, err == nil && id > 0
}

// playerKey is the baseline projection's key for the player, else a key
// built from the player's IDs, which Apply reports as not projected.
func (c *converter) playerKey(p news.Identity) string {
	if key, ok := c.byNHLID[p.NHLPlayerID]; ok && p.NHLPlayerID != 0 {
		return key
	}
	if key, ok := c.byYahooID[p.YahooPlayerID]; ok && p.YahooPlayerID != 0 {
		return key
	}
	if p.NHLPlayerID != 0 {
		return fmt.Sprintf("nhl:%d", p.NHLPlayerID)
	}
	return fmt.Sprintf("yahoo:%d", p.YahooPlayerID)
}

func extractedID(id int64) string {
	return fmt.Sprintf("%s%d", extractedIDPrefix, id)
}

// holds are the reasons an event may not drive numeric effects whatever its
// content: review and the extractor's evaluation.
func (c *converter) holds(e *ExtractedEvent) []string {
	var holds []string
	if e.Event.NeedsReview {
		holds = append(holds, "routed to review: "+e.Event.ReviewReason)
	}
	if !c.extraction.Released[e.ExtractorKey] {
		holds = append(holds, fmt.Sprintf("extractor %q has not passed its evaluation, so its events have no automatic effect", e.ExtractorKey))
	}
	return holds
}

func mapStatus(s newsevent.ReportStatus) ReportStatus {
	switch s {
	case newsevent.StatusConfirmed:
		return StatusConfirmed
	case newsevent.StatusReported:
		return StatusReported
	default:
		return StatusRumor
	}
}

// durationKindMap maps extraction durations that carry no number, or a game
// count, to adjustment durations; days and dates become an end time.
var durationKindMap = map[newsevent.DurationKind]DurationKind{
	newsevent.DurationUnknown:      DurationUnknown,
	newsevent.DurationIndefinite:   DurationIndefinite,
	newsevent.DurationGames:        DurationGames,
	newsevent.DurationDayToDay:     DurationDayToDay,
	newsevent.DurationWeekToWeek:   DurationWeekToWeek,
	newsevent.DurationMonthToMonth: DurationMonthToMonth,
	newsevent.DurationSeason:       DurationSeason,
}

// mapDuration sets the event's duration and, for a stated length in days
// or an end date, its end time. It returns a hold reason when the stated
// end does not follow the start.
func mapDuration(ev *Event, d newsevent.Duration) string {
	switch d.Kind {
	case newsevent.DurationDays:
		ev.Duration = Duration{Kind: DurationUntil}
		ev.EffectiveUntil = ev.start().Add(time.Duration(d.Days) * hoursPerDay * time.Hour)
	case newsevent.DurationUntilDate:
		ev.Duration = Duration{Kind: DurationUntil}
		ev.EffectiveUntil = d.Until
	default:
		kind, known := durationKindMap[d.Kind]
		if !known {
			ev.Duration = Duration{Kind: DurationUnknown}
			return fmt.Sprintf("unknown duration kind %q", d.Kind)
		}
		ev.Duration = Duration{Kind: kind}
		if kind == DurationGames {
			ev.Duration.Games = d.Games
		}
	}
	if !ev.EffectiveUntil.IsZero() && !ev.EffectiveUntil.After(ev.start()) {
		return "the stated end does not follow the start"
	}
	return ""
}

func joinHolds(holds []string) string {
	return strings.Join(holds, holdSeparator)
}
