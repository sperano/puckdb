package newsevent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// --- StoreQueries: the news_events table and its evidence/transitions ---

func (db *fakeDB) ListNewsEventsNear(_ context.Context, arg sqlcdb.ListNewsEventsNearParams) ([]sqlcdb.NewsEvent, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []sqlcdb.NewsEvent
	for _, e := range db.events {
		named := (e.NhlPlayerID.Valid && contains64(arg.NhlPlayerIds, e.NhlPlayerID.Int64)) ||
			(e.YahooPlayerID.Valid && contains32(arg.YahooPlayerIds, e.YahooPlayerID.Int32))
		recent := e.Lifecycle == string(LifecycleActive) || !e.LastReportedAt.Time.Before(arg.Since.Time)
		if (named && recent) || db.eventSupportsArticle(e.ID, arg.ArticleID) {
			out = append(out, *e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].FirstReportedAt.Time.Equal(out[j].FirstReportedAt.Time) {
			return out[i].FirstReportedAt.Time.Before(out[j].FirstReportedAt.Time)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (db *fakeDB) eventSupportsArticle(eventID, articleID int64) bool {
	for _, ev := range db.evidence {
		if ev.EventID == eventID && ev.Relation == string(RelationSupports) && ev.ArticleID == articleID {
			return true
		}
	}
	return false
}

func (db *fakeDB) ListNewsEventSupport(_ context.Context, eventIDs []int64) ([]sqlcdb.ListNewsEventSupportRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	seen := make(map[string]bool)
	var out []sqlcdb.ListNewsEventSupportRow
	for _, ev := range db.evidence {
		if ev.Relation != string(RelationSupports) || !contains64(eventIDs, ev.EventID) {
			continue
		}
		key := fmt.Sprintf("%d|%d|%d", ev.EventID, ev.ArticleID, ev.VersionID)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, sqlcdb.ListNewsEventSupportRow{
			EventID: ev.EventID, ArticleID: ev.ArticleID, VersionID: ev.VersionID, Publisher: ev.Publisher, Kind: ev.Kind,
		})
	}
	return out, nil
}

func (db *fakeDB) CreateNewsEvent(_ context.Context, arg sqlcdb.CreateNewsEventParams) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	id := db.nextEventID
	db.nextEventID++
	db.events = append(db.events, &sqlcdb.NewsEvent{
		ID: id, NhlPlayerID: arg.NhlPlayerID, YahooPlayerID: arg.YahooPlayerID, PlayerName: arg.PlayerName,
		EventType: arg.EventType, ReportStatus: arg.ReportStatus, Attribution: arg.Attribution,
		EffectiveFrom: arg.EffectiveFrom, DurationKind: arg.DurationKind, DurationGames: arg.DurationGames,
		DurationDays: arg.DurationDays, DurationUntil: arg.DurationUntil, ChangeField: arg.ChangeField,
		ChangeFrom: arg.ChangeFrom, ChangeTo: arg.ChangeTo, Lifecycle: arg.Lifecycle, SupersededBy: arg.SupersededBy,
		LifecycleReason: arg.LifecycleReason, LifecycleChangedAt: arg.LifecycleChangedAt, IncidentID: arg.IncidentID,
		FirstReportedAt: arg.FirstReportedAt, LastReportedAt: arg.FirstReportedAt, NeedsReview: arg.NeedsReview,
		ReviewReason: arg.ReviewReason, ExtractionID: arg.ExtractionID,
	})
	return id, nil
}

func (db *fakeDB) TouchNewsEvent(_ context.Context, arg sqlcdb.TouchNewsEventParams) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	e := db.eventByID(arg.ID)
	if e == nil {
		return fmt.Errorf("fakeDB: no event %d", arg.ID)
	}
	if arg.ReportedAt.Time.Before(e.FirstReportedAt.Time) {
		e.FirstReportedAt = arg.ReportedAt
	}
	if arg.ReportedAt.Time.After(e.LastReportedAt.Time) {
		e.LastReportedAt = arg.ReportedAt
	}
	return nil
}

func (db *fakeDB) SetNewsEventLifecycle(_ context.Context, arg sqlcdb.SetNewsEventLifecycleParams) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	e := db.eventByID(arg.ID)
	if e == nil {
		return fmt.Errorf("fakeDB: no event %d", arg.ID)
	}
	e.Lifecycle, e.LifecycleReason, e.LifecycleChangedAt, e.SupersededBy = arg.Lifecycle, arg.LifecycleReason, arg.LifecycleChangedAt, arg.SupersededBy
	return nil
}

func (db *fakeDB) FlagNewsEventReview(_ context.Context, arg sqlcdb.FlagNewsEventReviewParams) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	e := db.eventByID(arg.ID)
	if e == nil {
		return fmt.Errorf("fakeDB: no event %d", arg.ID)
	}
	e.NeedsReview = true
	switch {
	case e.ReviewReason == "":
		e.ReviewReason = arg.Reason
	case !strings.Contains(e.ReviewReason, arg.Reason):
		e.ReviewReason += reviewSeparator + arg.Reason
	}
	return nil
}

func (db *fakeDB) InsertNewsEventEvidence(_ context.Context, arg sqlcdb.InsertNewsEventEvidenceParams) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, ev := range db.evidence {
		if ev.EventID == arg.EventID && ev.VersionID == arg.VersionID && ev.ExtractionID == arg.ExtractionID && ev.Relation == arg.Relation {
			return 0, nil
		}
	}
	db.evidence = append(db.evidence, arg)
	return 1, nil
}

func (db *fakeDB) InsertNewsEventTransition(_ context.Context, arg sqlcdb.InsertNewsEventTransitionParams) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	id := db.nextTransitionID
	db.nextTransitionID++
	db.transitions = append(db.transitions, sqlcdb.NewsEventTransition{
		ID: id, EventID: arg.EventID, FromLifecycle: arg.FromLifecycle, ToLifecycle: arg.ToLifecycle,
		VersionID: arg.VersionID, ExtractionID: arg.ExtractionID, Reason: arg.Reason, At: arg.At,
	})
	return nil
}

func (db *fakeDB) GetNewsVersionIncident(_ context.Context, arg sqlcdb.GetNewsVersionIncidentParams) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	best := int64(0)
	found := false
	for _, inc := range db.incidents {
		if inc.VersionID != arg.VersionID || inc.Category != arg.Category {
			continue
		}
		nhlMatch := inc.NhlPlayerID.Valid && arg.NhlPlayerID.Valid && inc.NhlPlayerID.Int64 == arg.NhlPlayerID.Int64
		yahooMatch := inc.YahooPlayerID.Valid && arg.YahooPlayerID.Valid && inc.YahooPlayerID.Int32 == arg.YahooPlayerID.Int32
		if !nhlMatch && !yahooMatch {
			continue
		}
		if !found || inc.IncidentID < best {
			best, found = inc.IncidentID, true
		}
	}
	if !found {
		return 0, pgx.ErrNoRows
	}
	return best, nil
}

// --- ReportQueries ---

func (db *fakeDB) ListRecentNewsEvents(_ context.Context, arg sqlcdb.ListRecentNewsEventsParams) ([]sqlcdb.NewsEvent, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []sqlcdb.NewsEvent
	for _, e := range db.events {
		if !e.LastReportedAt.Time.Before(arg.LastReportedAt.Time) {
			out = append(out, *e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastReportedAt.Time.Equal(out[j].LastReportedAt.Time) {
			return out[i].LastReportedAt.Time.After(out[j].LastReportedAt.Time)
		}
		return out[i].ID > out[j].ID
	})
	if int(arg.Limit) > 0 && len(out) > int(arg.Limit) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (db *fakeDB) ListNewsEventsForPlayer(_ context.Context, arg sqlcdb.ListNewsEventsForPlayerParams) ([]sqlcdb.NewsEvent, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []sqlcdb.NewsEvent
	for _, e := range db.events {
		nhlMatch := arg.NhlPlayerID.Valid && e.NhlPlayerID.Valid && arg.NhlPlayerID.Int64 == e.NhlPlayerID.Int64
		yahooMatch := arg.YahooPlayerID.Valid && e.YahooPlayerID.Valid && arg.YahooPlayerID.Int32 == e.YahooPlayerID.Int32
		if nhlMatch || yahooMatch {
			out = append(out, *e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].FirstReportedAt.Time.Equal(out[j].FirstReportedAt.Time) {
			return out[i].FirstReportedAt.Time.Before(out[j].FirstReportedAt.Time)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (db *fakeDB) ListNewsEventEvidence(_ context.Context, eventIDs []int64) ([]sqlcdb.ListNewsEventEvidenceRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []sqlcdb.ListNewsEventEvidenceRow
	for _, ev := range db.evidence {
		if !contains64(eventIDs, ev.EventID) {
			continue
		}
		info := db.versions[ev.VersionID]
		out = append(out, sqlcdb.ListNewsEventEvidenceRow{
			EventID: ev.EventID, Relation: ev.Relation, ReportedAt: ev.ReportedAt, Publisher: ev.Publisher, Kind: ev.Kind,
			Quotes: ev.Quotes, ExtractionID: ev.ExtractionID, VersionID: ev.VersionID, Version: info.Version,
			Title: info.Title, Url: info.URL,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.EventID != b.EventID {
			return a.EventID < b.EventID
		}
		if !a.ReportedAt.Time.Equal(b.ReportedAt.Time) {
			return a.ReportedAt.Time.Before(b.ReportedAt.Time)
		}
		if a.VersionID != b.VersionID {
			return a.VersionID < b.VersionID
		}
		return a.Relation < b.Relation
	})
	return out, nil
}

func (db *fakeDB) ListNewsEventTransitions(_ context.Context, eventIDs []int64) ([]sqlcdb.NewsEventTransition, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []sqlcdb.NewsEventTransition
	for _, t := range db.transitions {
		if contains64(eventIDs, t.EventID) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.EventID != b.EventID {
			return a.EventID < b.EventID
		}
		if !a.At.Time.Equal(b.At.Time) {
			return a.At.Time.Before(b.At.Time)
		}
		return a.ID < b.ID
	})
	return out, nil
}

func (db *fakeDB) ListNewsExtractionReviews(_ context.Context, arg sqlcdb.ListNewsExtractionReviewsParams) ([]sqlcdb.ListNewsExtractionReviewsRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	latest := db.latestVersionByArticle()
	var out []sqlcdb.ListNewsExtractionReviewsRow
	for _, x := range db.extractions {
		if !db.reviewCandidate(x, arg, latest) {
			continue
		}
		info := db.versions[x.VersionID]
		out = append(out, sqlcdb.ListNewsExtractionReviewsRow{
			ID: x.ID, VersionID: x.VersionID, ExtractorKey: x.ExtractorKey, Status: x.Status, Attempts: x.Attempts,
			LastAttemptAt: x.LastAttemptAt, LastError: x.LastError, Issues: x.Issues,
			Title: info.Title, Url: info.URL, Publisher: info.Publisher,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastAttemptAt.Time.Equal(out[j].LastAttemptAt.Time) {
			return out[i].LastAttemptAt.Time.After(out[j].LastAttemptAt.Time)
		}
		return out[i].ID > out[j].ID
	})
	if int(arg.MaxRows) > 0 && len(out) > int(arg.MaxRows) {
		out = out[:arg.MaxRows]
	}
	return out, nil
}

// latestVersionByArticle returns, per article, the highest registered
// version number: ListNewsExtractionReviews only looks at an article's
// latest version. Caller holds db.mu.
func (db *fakeDB) latestVersionByArticle() map[int64]int32 {
	latest := make(map[int64]int32)
	for _, info := range db.versions {
		if info.Version > latest[info.ArticleID] {
			latest[info.ArticleID] = info.Version
		}
	}
	return latest
}

func (db *fakeDB) reviewCandidate(x *sqlcdb.NewsExtraction, arg sqlcdb.ListNewsExtractionReviewsParams, latest map[int64]int32) bool {
	if x.LastAttemptAt.Time.Before(arg.Since.Time) {
		return false
	}
	info, ok := db.versions[x.VersionID]
	if !ok || info.Version != latest[info.ArticleID] {
		return false
	}
	if x.Status == string(ExtractionInvalid) {
		return true
	}
	if (x.Status == string(ExtractionPending) || x.Status == string(ExtractionFailed)) && x.Attempts >= arg.MaxAttempts {
		return true
	}
	var issues []Issue
	_ = json.Unmarshal(x.Issues, &issues)
	return len(issues) > 0
}

// --- EvaluationQueries ---

func (db *fakeDB) InsertNewsExtractionEvaluation(_ context.Context, arg sqlcdb.InsertNewsExtractionEvaluationParams) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	id := db.nextEvaluationID
	db.nextEvaluationID++
	db.evaluations = append(db.evaluations, sqlcdb.NewsExtractionEvaluation{
		ID: id, ExtractorKey: arg.ExtractorKey, CorpusVersion: arg.CorpusVersion, Cases: arg.Cases,
		Passed: arg.Passed, Metrics: arg.Metrics, RunAt: arg.RunAt,
	})
	return id, nil
}

func (db *fakeDB) GetLatestNewsExtractionEvaluation(_ context.Context, arg sqlcdb.GetLatestNewsExtractionEvaluationParams) (sqlcdb.NewsExtractionEvaluation, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var best *sqlcdb.NewsExtractionEvaluation
	for i, e := range db.evaluations {
		if e.ExtractorKey != arg.ExtractorKey || e.CorpusVersion != arg.CorpusVersion {
			continue
		}
		if best == nil || e.RunAt.Time.After(best.RunAt.Time) || (e.RunAt.Time.Equal(best.RunAt.Time) && e.ID > best.ID) {
			best = &db.evaluations[i]
		}
	}
	if best == nil {
		return sqlcdb.NewsExtractionEvaluation{}, pgx.ErrNoRows
	}
	return *best, nil
}

var _ ReportQueries = (*fakeDB)(nil)
var _ EvaluationQueries = (*fakeDB)(nil)
