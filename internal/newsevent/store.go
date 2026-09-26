package newsevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// reviewSeparator joins a new event's review reasons.
const reviewSeparator = "; "

// StoreQueries is the database access reconciling needs. Callers run it in
// one transaction per report so a retry never half-applies one.
type StoreQueries interface {
	ListNewsEventsNear(ctx context.Context, arg sqlcdb.ListNewsEventsNearParams) ([]sqlcdb.NewsEvent, error)
	ListNewsEventSupport(ctx context.Context, eventIDs []int64) ([]sqlcdb.ListNewsEventSupportRow, error)
	CreateNewsEvent(ctx context.Context, arg sqlcdb.CreateNewsEventParams) (int64, error)
	TouchNewsEvent(ctx context.Context, arg sqlcdb.TouchNewsEventParams) error
	SetNewsEventLifecycle(ctx context.Context, arg sqlcdb.SetNewsEventLifecycleParams) error
	FlagNewsEventReview(ctx context.Context, arg sqlcdb.FlagNewsEventReviewParams) error
	InsertNewsEventEvidence(ctx context.Context, arg sqlcdb.InsertNewsEventEvidenceParams) (int64, error)
	InsertNewsEventTransition(ctx context.Context, arg sqlcdb.InsertNewsEventTransitionParams) error
	GetNewsVersionIncident(ctx context.Context, arg sqlcdb.GetNewsVersionIncidentParams) (int64, error)
}

// LoadNear loads the events a report is reconciled against: the players'
// active events, their other events reported within window before it (or
// later), and every event its article supports.
func LoadNear(ctx context.Context, q StoreQueries, players []news.Identity, r Report, window time.Duration) ([]StoredEvent, error) {
	params := sqlcdb.ListNewsEventsNearParams{
		NhlPlayerIds: []int64{}, YahooPlayerIds: []int32{},
		Since: news.Timestamptz(r.ReportedAt.Add(-window)), ArticleID: r.ArticleID,
	}
	for _, p := range players {
		if p.NHLPlayerID != 0 {
			params.NhlPlayerIds = append(params.NhlPlayerIds, p.NHLPlayerID)
		}
		if p.YahooPlayerID != 0 {
			params.YahooPlayerIds = append(params.YahooPlayerIds, int32(p.YahooPlayerID))
		}
	}
	rows, err := q.ListNewsEventsNear(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list news events near version %d: %w", r.VersionID, err)
	}
	events := make([]StoredEvent, len(rows))
	ids := make([]int64, len(rows))
	index := make(map[int64]int, len(rows))
	for i, row := range rows {
		events[i], ids[i], index[row.ID] = eventFromRow(row), row.ID, i
	}
	if len(ids) == 0 {
		return events, nil
	}
	support, err := q.ListNewsEventSupport(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list news event support: %w", err)
	}
	for _, s := range support {
		ev := &events[index[s.EventID]]
		ev.Support = append(ev.Support, Support{ArticleID: s.ArticleID, VersionID: s.VersionID, Publisher: s.Publisher, Kind: news.Kind(s.Kind)})
	}
	return events, nil
}

// EventFromRow reads a stored event's facts.
func EventFromRow(row sqlcdb.NewsEvent) Event {
	return Event{
		Player: news.Identity{
			NHLPlayerID: row.NhlPlayerID.Int64, YahooPlayerID: int(row.YahooPlayerID.Int32), Name: row.PlayerName,
		},
		Type: Type(row.EventType), Status: ReportStatus(row.ReportStatus), Attribution: row.Attribution,
		EffectiveOn: dateOf(row.EffectiveFrom),
		Duration: Duration{
			Kind: DurationKind(row.DurationKind), Games: int(row.DurationGames.Int32), Days: int(row.DurationDays.Int32),
			Until: dateOf(row.DurationUntil),
		},
		Change:      Change{Field: ChangeField(row.ChangeField), From: row.ChangeFrom, To: row.ChangeTo},
		NeedsReview: row.NeedsReview, ReviewReason: row.ReviewReason,
	}
}

func eventFromRow(row sqlcdb.NewsEvent) StoredEvent {
	return StoredEvent{
		ID: row.ID, Event: EventFromRow(row), Lifecycle: Lifecycle(row.Lifecycle),
		FirstReportedAt: row.FirstReportedAt.Time.UTC(), LastReportedAt: row.LastReportedAt.Time.UTC(),
	}
}

func dateOf(d pgtype.Date) time.Time {
	if !d.Valid {
		return time.Time{}
	}
	return d.Time
}

func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: !t.IsZero()}
}

func pgInt4(n int) pgtype.Int4 {
	return pgtype.Int4{Int32: int32(n), Valid: n != 0}
}

func pgInt8(n int64) pgtype.Int8 {
	return pgtype.Int8{Int64: n, Valid: n != 0}
}

// Apply records a plan: new events first, then evidence, lifecycle changes,
// span updates and review flags, each lifecycle change with its audit row.
func Apply(ctx context.Context, q StoreQueries, plan Plan, r Report, now time.Time) (Outcome, error) {
	var out Outcome
	ids := make([]int64, len(plan.Create))
	for k, n := range plan.Create {
		id, err := createEvent(ctx, q, n, r, now)
		if err != nil {
			return out, err
		}
		ids[k] = id
		countCreated(&out, n)
		out.Reviews += len(n.Review)
	}
	resolve := func(ref EventRef) int64 {
		if ref.New > 0 {
			return ids[ref.New-1]
		}
		return ref.ID
	}
	if err := applyEvidence(ctx, q, plan.Evidence, r, resolve, &out); err != nil {
		return out, err
	}
	for _, t := range plan.Transitions {
		if err := applyTransition(ctx, q, t, resolve(t.SupersededBy), r, now); err != nil {
			return out, err
		}
		out.countTransition(t.To)
	}
	for _, id := range plan.Touch {
		if err := q.TouchNewsEvent(ctx, sqlcdb.TouchNewsEventParams{ID: id, ReportedAt: news.Timestamptz(r.ReportedAt)}); err != nil {
			return out, fmt.Errorf("widen news event %d: %w", id, err)
		}
	}
	for _, f := range plan.Reviews {
		if err := q.FlagNewsEventReview(ctx, sqlcdb.FlagNewsEventReviewParams{ID: f.EventID, Reason: f.Reason}); err != nil {
			return out, fmt.Errorf("flag news event %d: %w", f.EventID, err)
		}
		out.Reviews++
	}
	return out, nil
}

func createEvent(ctx context.Context, q StoreQueries, n NewEvent, r Report, now time.Time) (int64, error) {
	e := n.Event
	nhlID, yahooID := pgInt8(e.Player.NHLPlayerID), pgInt4(e.Player.YahooPlayerID)
	incident, err := q.GetNewsVersionIncident(ctx, sqlcdb.GetNewsVersionIncidentParams{
		VersionID: r.VersionID, Category: string(e.Type), NhlPlayerID: nhlID, YahooPlayerID: yahooID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("find incident of version %d: %w", r.VersionID, err)
	}
	id, err := q.CreateNewsEvent(ctx, sqlcdb.CreateNewsEventParams{
		NhlPlayerID: nhlID, YahooPlayerID: yahooID, PlayerName: e.Player.Name,
		EventType: string(e.Type), ReportStatus: string(e.Status), Attribution: e.Attribution,
		EffectiveFrom: pgDate(e.EffectiveOn), DurationKind: string(e.Duration.Kind),
		DurationGames: pgInt4(e.Duration.Games), DurationDays: pgInt4(e.Duration.Days), DurationUntil: pgDate(e.Duration.Until),
		ChangeField: string(e.Change.Field), ChangeFrom: e.Change.From, ChangeTo: e.Change.To,
		Lifecycle: string(n.Lifecycle), SupersededBy: pgInt8(n.SupersededBy), LifecycleReason: n.Reason,
		LifecycleChangedAt: news.Timestamptz(now), IncidentID: pgInt8(incident), FirstReportedAt: news.Timestamptz(r.ReportedAt),
		NeedsReview: len(n.Review) > 0, ReviewReason: strings.Join(n.Review, reviewSeparator), ExtractionID: pgInt8(r.ExtractionID),
	})
	if err != nil {
		return 0, fmt.Errorf("create %s event for %s: %w", e.Type, e.Player.Key(), err)
	}
	err = q.InsertNewsEventTransition(ctx, sqlcdb.InsertNewsEventTransitionParams{
		EventID: id, FromLifecycle: "", ToLifecycle: string(n.Lifecycle), VersionID: pgInt8(r.VersionID),
		ExtractionID: pgInt8(r.ExtractionID), Reason: n.Reason, At: news.Timestamptz(now),
	})
	if err != nil {
		return 0, fmt.Errorf("record creation of news event %d: %w", id, err)
	}
	return id, nil
}

func applyEvidence(ctx context.Context, q StoreQueries, links []EvidenceLink, r Report, resolve func(EventRef) int64, out *Outcome) error {
	for _, link := range links {
		quotes := link.Quotes
		if quotes == nil {
			quotes = []Quote{}
		}
		encoded, err := json.Marshal(quotes)
		if err != nil {
			return fmt.Errorf("encode quotes of version %d: %w", r.VersionID, err)
		}
		id := resolve(link.Ref)
		added, err := q.InsertNewsEventEvidence(ctx, sqlcdb.InsertNewsEventEvidenceParams{
			EventID: id, VersionID: r.VersionID, ExtractionID: r.ExtractionID, Relation: string(link.Relation),
			ArticleID: r.ArticleID, Publisher: r.Publisher, Kind: string(r.Kind), ReportedAt: news.Timestamptz(r.ReportedAt),
			Quotes: encoded,
		})
		if err != nil {
			return fmt.Errorf("attach version %d to news event %d: %w", r.VersionID, id, err)
		}
		if added > 0 && link.Ref.New == 0 && link.Relation == RelationSupports {
			out.Supported++
		}
	}
	return nil
}

func applyTransition(ctx context.Context, q StoreQueries, t Transition, supersededBy int64, r Report, now time.Time) error {
	err := q.SetNewsEventLifecycle(ctx, sqlcdb.SetNewsEventLifecycleParams{
		ID: t.EventID, Lifecycle: string(t.To), LifecycleReason: t.Reason,
		LifecycleChangedAt: news.Timestamptz(now), SupersededBy: pgInt8(supersededBy),
	})
	if err != nil {
		return fmt.Errorf("set lifecycle of news event %d: %w", t.EventID, err)
	}
	err = q.InsertNewsEventTransition(ctx, sqlcdb.InsertNewsEventTransitionParams{
		EventID: t.EventID, FromLifecycle: string(t.From), ToLifecycle: string(t.To), VersionID: pgInt8(r.VersionID),
		ExtractionID: pgInt8(r.ExtractionID), Reason: t.Reason, At: news.Timestamptz(now),
	})
	if err != nil {
		return fmt.Errorf("record lifecycle of news event %d: %w", t.EventID, err)
	}
	return nil
}
