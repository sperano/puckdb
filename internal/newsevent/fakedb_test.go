package newsevent

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// firstFakeID is the first autoincrement ID the fake hands out for any
// table: real sequences start at 1, and code under test (e.g. Apply's use
// of pgInt8) treats 0 as "no row", so starting fakes at 0 would be a false
// friend.
const firstFakeID = 1

// fakeVersionInfo is what the fake remembers about a news_article_versions
// row: enough for the joins ListNewsArticleEventPlayers, ListNewsEventEvidence
// and ListNewsExtractionReviews perform in the real schema.
type fakeVersionInfo struct {
	ArticleID int64
	Version   int32
	Title     string
	URL       string
	Publisher string
}

// fakeIncident is a seeded row of news_incidents joined to
// news_incident_evidence, as GetNewsVersionIncident reads it.
type fakeIncident struct {
	VersionID     int64
	Category      string
	NhlPlayerID   pgtype.Int8
	YahooPlayerID pgtype.Int4
	IncidentID    int64
}

// fakeDB is an in-memory stand-in for every sqlc query interface the
// newsevent package needs (RunQueries, TxQueries/StoreQueries,
// ReportQueries, EvaluationQueries), modeled closely enough on
// internal/sqlcdb/queries/news_events.sql that the package's own reconciling
// logic can run against it unmodified.
//
// All state is guarded by one mutex; InTx serializes callers the same way
// the news event lock does in Postgres.
type fakeDB struct {
	mu sync.Mutex

	versions       map[int64]fakeVersionInfo
	versionPlayers map[int64][]sqlcdb.ListNewsVersionPlayersRow
	incidents      []fakeIncident

	nextExtractionID int64
	extractions      []*sqlcdb.NewsExtraction

	nextEventID int64
	events      []*sqlcdb.NewsEvent

	evidence []sqlcdb.InsertNewsEventEvidenceParams

	nextTransitionID int64
	transitions      []sqlcdb.NewsEventTransition

	nextEvaluationID int64
	evaluations      []sqlcdb.NewsExtractionEvaluation

	inTxCalls int
	// txErr, when set, fails every transaction (a reconcile that keeps
	// failing).
	txErr error
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		versions:         make(map[int64]fakeVersionInfo),
		versionPlayers:   make(map[int64][]sqlcdb.ListNewsVersionPlayersRow),
		nextExtractionID: firstFakeID,
		nextEventID:      firstFakeID,
		nextTransitionID: firstFakeID,
		nextEvaluationID: firstFakeID,
	}
}

// --- test-side seeding helpers (not part of any sqlc interface) ---

// addVersion registers a version's article/publisher/title/url so the joins
// in ListNewsArticleEventPlayers, ListNewsEventEvidence and
// ListNewsExtractionReviews can resolve it.
func (db *fakeDB) addVersion(id int64, info fakeVersionInfo) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.versions[id] = info
}

// addVersionFromRow registers a ListNewsVersionsToExtractRow the way
// addVersion would, for tests that already built one for Runner.Run.
func (db *fakeDB) addVersionFromRow(row sqlcdb.ListNewsVersionsToExtractRow) {
	db.addVersion(row.ID, fakeVersionInfo{
		ArticleID: row.ArticleID, Version: row.Version, Title: row.Title, URL: row.Url, Publisher: row.Publisher,
	})
}

// setVersionPlayers seeds the resolved mentions ListNewsVersionPlayers
// returns for a version.
func (db *fakeDB) setVersionPlayers(versionID int64, rows ...sqlcdb.ListNewsVersionPlayersRow) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.versionPlayers[versionID] = rows
}

// addIncident seeds a news_incidents row GetNewsVersionIncident may return.
func (db *fakeDB) addIncident(versionID int64, category string, nhlID int64, yahooID int, incidentID int64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.incidents = append(db.incidents, fakeIncident{
		VersionID: versionID, Category: category, NhlPlayerID: pgInt8(nhlID), YahooPlayerID: pgInt4(yahooID), IncidentID: incidentID,
	})
}

// seedExtraction inserts a news_extractions row directly (a crash-recovery
// or model-change fixture), bypassing StartNewsExtraction's attempt counter.
func (db *fakeDB) seedExtraction(x sqlcdb.NewsExtraction) sqlcdb.NewsExtraction {
	db.mu.Lock()
	defer db.mu.Unlock()
	if x.ID == 0 {
		x.ID = db.nextExtractionID
	}
	if x.ID >= db.nextExtractionID {
		db.nextExtractionID = x.ID + 1
	}
	stored := x
	db.extractions = append(db.extractions, &stored)
	return stored
}

// eventByID finds a stored event, or nil. Caller holds db.mu.
func (db *fakeDB) eventByID(id int64) *sqlcdb.NewsEvent {
	for _, e := range db.events {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// event returns a copy of a stored event's row, for assertions.
func (db *fakeDB) event(id int64) (sqlcdb.NewsEvent, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	e := db.eventByID(id)
	if e == nil {
		return sqlcdb.NewsEvent{}, false
	}
	return *e, true
}

// eventCount is the number of stored events, for assertions.
func (db *fakeDB) eventCount() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return len(db.events)
}

// evidenceFor returns the evidence rows recorded for one event, for
// assertions.
func (db *fakeDB) evidenceFor(eventID int64) []sqlcdb.InsertNewsEventEvidenceParams {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []sqlcdb.InsertNewsEventEvidenceParams
	for _, ev := range db.evidence {
		if ev.EventID == eventID {
			out = append(out, ev)
		}
	}
	return out
}

// transitionsFor returns the lifecycle transitions recorded for one event.
func (db *fakeDB) transitionsFor(eventID int64) []sqlcdb.NewsEventTransition {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []sqlcdb.NewsEventTransition
	for _, t := range db.transitions {
		if t.EventID == eventID {
			out = append(out, t)
		}
	}
	return out
}

// extractionsFor returns every extraction recorded for one version, in
// insertion order (the audit trail).
func (db *fakeDB) extractionsFor(versionID int64) []sqlcdb.NewsExtraction {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []sqlcdb.NewsExtraction
	for _, x := range db.extractions {
		if x.VersionID == versionID {
			out = append(out, *x)
		}
	}
	return out
}

// InTx runs fn against db, holding the lock for the duration: a faithful
// enough stand-in for "one transaction that holds the news event lock".
func (db *fakeDB) InTx(_ context.Context, fn func(q TxQueries) error) error {
	db.mu.Lock()
	db.inTxCalls++
	txErr := db.txErr
	db.mu.Unlock()
	if txErr != nil {
		return txErr
	}
	return fn(db)
}

// --- RunQueries ---

func (db *fakeDB) ListNewsVersionPlayers(_ context.Context, versionID int64) ([]sqlcdb.ListNewsVersionPlayersRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return append([]sqlcdb.ListNewsVersionPlayersRow(nil), db.versionPlayers[versionID]...), nil
}

func (db *fakeDB) ListNewsArticleEventPlayers(_ context.Context, arg sqlcdb.ListNewsArticleEventPlayersParams) ([]sqlcdb.ListNewsArticleEventPlayersRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	seen := make(map[string]bool)
	var out []sqlcdb.ListNewsArticleEventPlayersRow
	for _, ev := range db.evidence {
		if ev.Relation != string(RelationSupports) {
			continue
		}
		info, ok := db.versions[ev.VersionID]
		if !ok || info.ArticleID != arg.ArticleID || info.Version >= arg.Version {
			continue
		}
		e := db.eventByID(ev.EventID)
		if e == nil {
			continue
		}
		key := fmt.Sprintf("%d|%d|%s", e.NhlPlayerID.Int64, e.YahooPlayerID.Int32, e.PlayerName)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, sqlcdb.ListNewsArticleEventPlayersRow{
			NhlPlayerID: e.NhlPlayerID, YahooPlayerID: e.YahooPlayerID, PlayerName: e.PlayerName,
		})
	}
	return out, nil
}

func (db *fakeDB) GetNewsExtraction(_ context.Context, arg sqlcdb.GetNewsExtractionParams) (sqlcdb.NewsExtraction, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, x := range db.extractions {
		if x.VersionID == arg.VersionID && x.ExtractorKey == arg.ExtractorKey {
			return *x, nil
		}
	}
	return sqlcdb.NewsExtraction{}, pgx.ErrNoRows
}

func (db *fakeDB) StartNewsExtraction(_ context.Context, arg sqlcdb.StartNewsExtractionParams) (sqlcdb.StartNewsExtractionRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, x := range db.extractions {
		if x.VersionID == arg.VersionID && x.ExtractorKey == arg.ExtractorKey {
			if x.Status == string(ExtractionPending) && x.LastAttemptAt.Time.After(arg.StaleBefore.Time) {
				// Another refresh started this attempt recently.
				return sqlcdb.StartNewsExtractionRow{}, pgx.ErrNoRows
			}
			x.InputHash = arg.InputHash
			x.Status = string(ExtractionPending)
			x.Attempts++
			x.LastAttemptAt = arg.LastAttemptAt
			return sqlcdb.StartNewsExtractionRow{ID: x.ID, Attempts: x.Attempts}, nil
		}
	}
	id := db.nextExtractionID
	db.nextExtractionID++
	x := &sqlcdb.NewsExtraction{
		ID: id, VersionID: arg.VersionID, ExtractorKey: arg.ExtractorKey, Provider: arg.Provider, Model: arg.Model,
		PromptVersion: arg.PromptVersion, SchemaVersion: arg.SchemaVersion, InputHash: arg.InputHash,
		Status: string(ExtractionPending), Attempts: 1, LastAttemptAt: arg.LastAttemptAt,
	}
	db.extractions = append(db.extractions, x)
	return sqlcdb.StartNewsExtractionRow{ID: id, Attempts: 1}, nil
}

func (db *fakeDB) FindCachedNewsExtraction(_ context.Context, arg sqlcdb.FindCachedNewsExtractionParams) (sqlcdb.FindCachedNewsExtractionRow, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var best *sqlcdb.NewsExtraction
	for _, x := range db.extractions {
		if x.ExtractorKey != arg.ExtractorKey || x.InputHash != arg.InputHash || x.Status != string(ExtractionSucceeded) {
			continue
		}
		if best == nil || x.ID < best.ID {
			best = x
		}
	}
	if best == nil {
		return sqlcdb.FindCachedNewsExtractionRow{}, pgx.ErrNoRows
	}
	return sqlcdb.FindCachedNewsExtractionRow{ID: best.ID, RawOutput: best.RawOutput}, nil
}

func (db *fakeDB) FinishNewsExtraction(_ context.Context, arg sqlcdb.FinishNewsExtractionParams) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, x := range db.extractions {
		if x.ID == arg.ID {
			x.Status, x.LastError, x.RawOutput = arg.Status, arg.LastError, arg.RawOutput
			x.Issues, x.Events = arg.Issues, arg.Events
			x.PromptTokens += arg.AddPromptTokens
			x.CompletionTokens += arg.AddCompletionTokens
			x.CachedFromID = arg.CachedFromID
			return nil
		}
	}
	return fmt.Errorf("fakeDB: no extraction %d", arg.ID)
}

// --- StoreQueries / TxQueries ---

func (db *fakeDB) MarkNewsExtractionReconciled(_ context.Context, arg sqlcdb.MarkNewsExtractionReconciledParams) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, x := range db.extractions {
		if x.ID == arg.ID {
			x.ReconciledAt, x.LastError = arg.ReconciledAt, ""
			return nil
		}
	}
	return fmt.Errorf("fakeDB: no extraction %d", arg.ID)
}

func (db *fakeDB) IsNewsExtractionReconciled(_ context.Context, id int64) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, x := range db.extractions {
		if x.ID == id {
			return x.ReconciledAt.Valid, nil
		}
	}
	return false, pgx.ErrNoRows
}

func (db *fakeDB) NoteNewsExtractionReconcileFailure(_ context.Context, arg sqlcdb.NoteNewsExtractionReconcileFailureParams) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, x := range db.extractions {
		if x.ID == arg.ID {
			x.Attempts++
			x.LastAttemptAt, x.LastError = arg.LastAttemptAt, arg.LastError
			return nil
		}
	}
	return fmt.Errorf("fakeDB: no extraction %d", arg.ID)
}

// ListNewsEventsNear, ListNewsEventSupport and the rest of StoreQueries
// (the news_events table itself) live in fakedb_events_test.go, to keep
// this file to extraction bookkeeping and the seeding/assertion helpers.

// --- small shared helpers ---

func contains64(xs []int64, v int64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func contains32(xs []int32, v int32) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

var _ RunQueries = (*fakeDB)(nil)
var _ TxQueries = (*fakeDB)(nil)
