package newsadjust

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ExtractedEventQueries reads stored extraction events, the NHL teams and
// the extractors' evaluations.
type ExtractedEventQueries interface {
	ListNewsEventsForAdjustment(ctx context.Context, arg sqlcdb.ListNewsEventsForAdjustmentParams) ([]sqlcdb.ListNewsEventsForAdjustmentRow, error)
	ListNewsEventAdjustmentEvidence(ctx context.Context, eventIDs []int64) ([]sqlcdb.ListNewsEventAdjustmentEvidenceRow, error)
	ListNewsEventTransitions(ctx context.Context, eventIDs []int64) ([]sqlcdb.NewsEventTransition, error)
	ListNewsAdjustmentTeams(ctx context.Context) ([]sqlcdb.ListNewsAdjustmentTeamsRow, error)
	newsevent.EvaluationQueries
}

// LoadEvents loads the stored extraction events about the baseline's
// players as adjustment event versions (LoadExtractedEvents).
func (r *Repository) LoadEvents(ctx context.Context, baseline projection.Snapshot) ([]Event, []string, error) {
	return LoadExtractedEvents(ctx, r.queries, baseline.Players)
}

// LoadExtractedEvents loads every stored extraction event about the players
// with its full history and converts it (ConvertExtracted). An extractor is
// released when its latest evaluation on the built-in corpus passed; that
// is read at load time, not per version. The warnings name events that
// could not be converted.
func LoadExtractedEvents(ctx context.Context, q ExtractedEventQueries, players []projection.PlayerProjection) ([]Event, []string, error) {
	rows, err := q.ListNewsEventsForAdjustment(ctx, playerIDs(players))
	if err != nil {
		return nil, nil, fmt.Errorf("list news events for adjustment: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}
	events, ids, err := extractedEvents(ctx, q, rows)
	if err != nil {
		return nil, nil, err
	}
	if err := attachTransitions(ctx, q, events, ids); err != nil {
		return nil, nil, err
	}
	teams, err := loadTeams(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	released, err := releasedExtractors(ctx, q, events)
	if err != nil {
		return nil, nil, err
	}
	converted, warnings := ConvertExtracted(events, Extraction{Players: players, Teams: teams, Released: released})
	return converted, warnings, nil
}

func playerIDs(players []projection.PlayerProjection) sqlcdb.ListNewsEventsForAdjustmentParams {
	params := sqlcdb.ListNewsEventsForAdjustmentParams{NhlPlayerIds: []int64{}, YahooPlayerIds: []int32{}}
	for _, p := range players {
		if p.PlayerID != nil {
			params.NhlPlayerIds = append(params.NhlPlayerIds, *p.PlayerID)
		}
		if id, ok := yahooPlayerID(p.PlayerKey); ok {
			params.YahooPlayerIds = append(params.YahooPlayerIds, int32(id))
		}
	}
	return params
}

// extractedEvents reads the events and their evidence; it returns the
// events and their IDs in ID order, as the query sorts them.
func extractedEvents(ctx context.Context, q ExtractedEventQueries, rows []sqlcdb.ListNewsEventsForAdjustmentRow) ([]ExtractedEvent, []int64, error) {
	events := make([]ExtractedEvent, len(rows))
	ids := make([]int64, len(rows))
	for i, row := range rows {
		events[i] = ExtractedEvent{
			ID: row.NewsEvent.ID, Event: newsevent.EventFromRow(row.NewsEvent),
			IncidentID: row.NewsEvent.IncidentID.Int64, ExtractorKey: row.ExtractorKey,
		}
		ids[i] = row.NewsEvent.ID
	}
	evidence, err := q.ListNewsEventAdjustmentEvidence(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("list news event evidence: %w", err)
	}
	for _, row := range evidence {
		i, found := slices.BinarySearch(ids, row.EventID)
		if !found {
			continue
		}
		var quotes []newsevent.Quote
		if err := json.Unmarshal(row.Quotes, &quotes); err != nil {
			return nil, nil, fmt.Errorf("decode quotes of news event %d version %d: %w", row.EventID, row.VersionID, err)
		}
		events[i].Evidence = append(events[i].Evidence, ExtractedEvidence{
			VersionID: row.VersionID, ExtractionID: row.ExtractionID, Relation: newsevent.Relation(row.Relation),
			Publisher: row.Publisher, Kind: news.Kind(row.Kind), URL: row.Url, ReportedAt: timeOf(row.ReportedAt),
			RetrievedAt: timeOf(row.RetrievedAt), AddedAt: timeOf(row.AddedAt), Quotes: quotes,
		})
	}
	return events, ids, nil
}

func attachTransitions(ctx context.Context, q ExtractedEventQueries, events []ExtractedEvent, ids []int64) error {
	rows, err := q.ListNewsEventTransitions(ctx, ids)
	if err != nil {
		return fmt.Errorf("list news event transitions: %w", err)
	}
	for _, row := range rows {
		i, found := slices.BinarySearch(ids, row.EventID)
		if !found {
			continue
		}
		events[i].Transitions = append(events[i].Transitions, ExtractedTransition{
			To: newsevent.Lifecycle(row.ToLifecycle), VersionID: row.VersionID.Int64,
			ExtractionID: row.ExtractionID.Int64, At: timeOf(row.At),
		})
	}
	return nil
}

func loadTeams(ctx context.Context, q ExtractedEventQueries) (TeamDirectory, error) {
	rows, err := q.ListNewsAdjustmentTeams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list NHL teams: %w", err)
	}
	teams := make([]Team, len(rows))
	for i, row := range rows {
		teams[i] = Team{ID: row.TeamID, Abbrev: row.Abbrev, FullName: row.FullName, CommonName: row.CommonName}
	}
	return NewTeamDirectory(teams), nil
}

// releasedExtractors asks the release gate about every extractor behind
// the events.
func releasedExtractors(ctx context.Context, q ExtractedEventQueries, events []ExtractedEvent) (map[string]bool, error) {
	released := make(map[string]bool)
	for _, e := range events {
		if _, checked := released[e.ExtractorKey]; checked || e.ExtractorKey == "" {
			continue
		}
		allowed, err := newsevent.AutomaticEffectsAllowed(ctx, q, e.ExtractorKey)
		if err != nil {
			return nil, fmt.Errorf("check release of extractor %s: %w", e.ExtractorKey, err)
		}
		released[e.ExtractorKey] = allowed
	}
	return released, nil
}
