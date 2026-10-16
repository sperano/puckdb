package newsevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// eventDetailQueries reads the evidence and history of stored events.
type eventDetailQueries interface {
	ListNewsEventEvidence(ctx context.Context, eventIDs []int64) ([]sqlcdb.ListNewsEventEvidenceRow, error)
	ListNewsEventTransitions(ctx context.Context, eventIDs []int64) ([]sqlcdb.NewsEventTransition, error)
}

// PlayerEventQueries is what LoadPlayerEvents reads.
type PlayerEventQueries interface {
	ListNewsEventsForPlayer(ctx context.Context, arg sqlcdb.ListNewsEventsForPlayerParams) ([]sqlcdb.NewsEvent, error)
	eventDetailQueries
}

// EventReportQueries is what the events report reads.
type EventReportQueries interface {
	PlayerEventQueries
	ListRecentNewsEvents(ctx context.Context, arg sqlcdb.ListRecentNewsEventsParams) ([]sqlcdb.NewsEvent, error)
	ListNewsExtractionReviews(ctx context.Context, arg sqlcdb.ListNewsExtractionReviewsParams) ([]sqlcdb.ListNewsExtractionReviewsRow, error)
}

// ReportedEvent is a stored event with its evidence and history.
type ReportedEvent struct {
	ID              int64
	Event           Event
	Lifecycle       Lifecycle
	LifecycleReason string
	SupersededBy    int64
	IncidentID      int64
	FirstReportedAt time.Time
	LastReportedAt  time.Time
	Evidence        []EventEvidence
	History         []sqlcdb.NewsEventTransition
}

// EventEvidence is one report behind an event.
type EventEvidence struct {
	Relation     Relation
	Publisher    string
	Kind         news.Kind
	ReportedAt   time.Time
	ExtractionID int64
	VersionID    int64
	Version      int
	Title        string
	URL          string
	Quotes       []Quote
}

// ExtractionReview is an extraction a person should look at.
type ExtractionReview struct {
	ID           int64
	VersionID    int64
	ExtractorKey string
	Status       ExtractionStatus
	Attempts     int
	LastAttempt  time.Time
	Error        string
	Issues       []Issue
	Title        string
	URL          string
	Publisher    string
}

// EventDigest is everything the events report prints.
type EventDigest struct {
	GeneratedAt time.Time
	Since       time.Time
	Events      []ReportedEvent
	// Player holds every event of the requested player, when one was asked.
	Player       *news.Identity
	PlayerEvents []ReportedEvent
	Reviews      []ExtractionReview
}

// DigestRequest is what the events report was asked for.
type DigestRequest struct {
	Since       time.Time
	Limit       int
	Player      news.Identity
	MaxAttempts int
}

// LoadEventDigest reads recent events, the requested player's events and
// the extractions that need review.
func LoadEventDigest(ctx context.Context, q EventReportQueries, req DigestRequest, now time.Time) (EventDigest, error) {
	d := EventDigest{GeneratedAt: now, Since: req.Since}
	rows, err := q.ListRecentNewsEvents(ctx, sqlcdb.ListRecentNewsEventsParams{
		LastReportedAt: news.Timestamptz(req.Since), Limit: int32(req.Limit),
	})
	if err != nil {
		return d, fmt.Errorf("list recent news events: %w", err)
	}
	if d.Events, err = loadReportedEvents(ctx, q, rows); err != nil {
		return d, err
	}
	if req.Player.NHLPlayerID != 0 || req.Player.YahooPlayerID != 0 {
		player := req.Player
		d.Player = &player
		selected, err := LoadPlayerEvents(ctx, q, PlayerEventsRequest{Player: player, IncludeInactive: true})
		if err != nil {
			return d, err
		}
		d.PlayerEvents = selected.Events
	}
	d.Reviews, err = loadReviews(ctx, q, req)
	return d, err
}

// PlayerEventsRequest selects one player's stored events.
type PlayerEventsRequest struct {
	// Player names the player by NHL ID, Yahoo ID or both (at least one
	// is required); an event stored under either ID matches, so passing
	// both also finds events recorded before the two IDs were matched.
	Player news.Identity
	// Since keeps events last reported at or after it; zero keeps all.
	Since time.Time
	// IncludeInactive keeps superseded, retracted and resolved events
	// too; otherwise only active ones are kept.
	IncludeInactive bool
	// Limit keeps the latest events (by first report); 0 keeps all.
	Limit int
}

// PlayerEvents is the selection of one player's events.
type PlayerEvents struct {
	// Matching counts the events the filters kept, before Limit.
	Matching int
	// Events are in first-reported order, oldest first.
	Events []ReportedEvent
}

// LoadPlayerEvents reads the requested player's events, filters them and
// loads the evidence and history of those it keeps.
func LoadPlayerEvents(ctx context.Context, q PlayerEventQueries, req PlayerEventsRequest) (PlayerEvents, error) {
	if req.Player.NHLPlayerID == 0 && req.Player.YahooPlayerID == 0 {
		return PlayerEvents{}, errors.New("list news events: an NHL or Yahoo player ID is required")
	}
	rows, err := q.ListNewsEventsForPlayer(ctx, sqlcdb.ListNewsEventsForPlayerParams{
		NhlPlayerID: pgInt8(req.Player.NHLPlayerID), YahooPlayerID: pgInt4(req.Player.YahooPlayerID),
	})
	if err != nil {
		return PlayerEvents{}, fmt.Errorf("list news events of %s: %w", req.Player.Key(), err)
	}
	rows = slices.DeleteFunc(slices.Clone(rows), func(row sqlcdb.NewsEvent) bool { return !req.keeps(row) })
	selected := PlayerEvents{Matching: len(rows)}
	if req.Limit > 0 && len(rows) > req.Limit {
		rows = rows[len(rows)-req.Limit:]
	}
	selected.Events, err = loadReportedEvents(ctx, q, rows)
	return selected, err
}

// keeps reports whether row passes the lifecycle and since filters.
func (req PlayerEventsRequest) keeps(row sqlcdb.NewsEvent) bool {
	if !req.IncludeInactive && Lifecycle(row.Lifecycle) != LifecycleActive {
		return false
	}
	return req.Since.IsZero() || !row.LastReportedAt.Time.Before(req.Since)
}

func loadReportedEvents(ctx context.Context, q eventDetailQueries, rows []sqlcdb.NewsEvent) ([]ReportedEvent, error) {
	events := make([]ReportedEvent, len(rows))
	ids := make([]int64, len(rows))
	index := make(map[int64]int, len(rows))
	for i, row := range rows {
		ids[i], index[row.ID] = row.ID, i
		events[i] = ReportedEvent{
			ID: row.ID, Event: EventFromRow(row), Lifecycle: Lifecycle(row.Lifecycle), LifecycleReason: row.LifecycleReason,
			SupersededBy: row.SupersededBy.Int64, IncidentID: row.IncidentID.Int64,
			FirstReportedAt: row.FirstReportedAt.Time, LastReportedAt: row.LastReportedAt.Time,
		}
	}
	if len(ids) == 0 {
		return events, nil
	}
	evidence, err := q.ListNewsEventEvidence(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list news event evidence: %w", err)
	}
	for _, e := range evidence {
		var quotes []Quote
		if err := json.Unmarshal(e.Quotes, &quotes); err != nil {
			return nil, fmt.Errorf("decode quotes of news event %d: %w", e.EventID, err)
		}
		ev := &events[index[e.EventID]]
		ev.Evidence = append(ev.Evidence, EventEvidence{
			Relation: Relation(e.Relation), Publisher: e.Publisher, Kind: news.Kind(e.Kind), ReportedAt: e.ReportedAt.Time,
			ExtractionID: e.ExtractionID, VersionID: e.VersionID, Version: int(e.Version), Title: e.Title, URL: e.Url, Quotes: quotes,
		})
	}
	history, err := q.ListNewsEventTransitions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list news event history: %w", err)
	}
	for _, h := range history {
		ev := &events[index[h.EventID]]
		ev.History = append(ev.History, h)
	}
	return events, nil
}

func loadReviews(ctx context.Context, q EventReportQueries, req DigestRequest) ([]ExtractionReview, error) {
	rows, err := q.ListNewsExtractionReviews(ctx, sqlcdb.ListNewsExtractionReviewsParams{
		Since: news.Timestamptz(req.Since), MaxAttempts: int32(req.MaxAttempts), MaxRows: int32(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list news extraction reviews: %w", err)
	}
	reviews := make([]ExtractionReview, 0, len(rows))
	for _, r := range rows {
		var issues []Issue
		if err := json.Unmarshal(r.Issues, &issues); err != nil {
			return nil, fmt.Errorf("decode issues of extraction %d: %w", r.ID, err)
		}
		reviews = append(reviews, ExtractionReview{
			ID: r.ID, VersionID: r.VersionID, ExtractorKey: r.ExtractorKey, Status: ExtractionStatus(r.Status),
			Attempts: int(r.Attempts), LastAttempt: r.LastAttemptAt.Time, Error: r.LastError, Issues: issues,
			Title: r.Title, URL: r.Url, Publisher: r.Publisher,
		})
	}
	return reviews, nil
}
