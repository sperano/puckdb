package draftwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const maxDraftEventPageSize = 200

var (
	// ErrStaleStateVersion means a versioned mutation's expected state
	// version is no longer the session's; StaleStateVersionError carries both.
	ErrStaleStateVersion = errors.New("draft state version is stale")
	// ErrSessionNotFound means no draft session row exists for a league key
	// yet; Ensure creates one.
	ErrSessionNotFound = errors.New("draft session not found")
)

// StaleStateVersionError reports the version expected by a mutation and the
// current version observed after taking the session row lock.
type StaleStateVersionError struct {
	Expected uint64
	Actual   uint64
}

func (e *StaleStateVersionError) Error() string {
	return fmt.Sprintf("%v: expected %d, current %d", ErrStaleStateVersion, e.Expected, e.Actual)
}

// Is makes errors.Is(err, ErrStaleStateVersion) match.
func (e *StaleStateVersionError) Is(target error) bool { return target == ErrStaleStateVersion }

// Session is the persisted status needed by CLI and later API consumers.
type Session struct {
	Identity            Identity
	State               draftsession.State
	DraftStatus         string
	RecommendationsSafe bool
	Complete            bool
	UpstreamPickCount   int
	SkippedPickCount    int
	LastPollAt          *time.Time
	LastSuccessAt       *time.Time
	LastAuthoritativeAt *time.Time
	LastError           string
	SyncVersion         uint64
	UpdatedAt           time.Time
}

// SafeToRecommend reports whether the stored safety flag and the board state
// both allow recommendations. The flag predates stricter board invariants on
// rows not rewritten since, so the reducer's verdict has to hold as well.
func (s Session) SafeToRecommend() bool {
	return s.RecommendationsSafe && draftsession.SafeToRecommend(s.State)
}

// Event is one persisted state-changing draft board event.
type Event struct {
	StateVersion uint64
	Kind         string
	Details      json.RawMessage
	CreatedAt    time.Time
}

// Observation is one stored poll result used by the capability report.
type Observation struct {
	PolledAt      time.Time
	Duration      time.Duration
	Success       bool
	Authoritative bool
	Changed       bool
	DraftStatus   string
	DeclaredCount int
	ParsedCount   int
	SkippedCount  int
	SnapshotHash  string
	ErrorClass    ErrorClass
	Error         string
}

// Repository persists each reducer transition and its observation atomically.
type Repository struct {
	pool    *pgxpool.Pool
	queries *sqlcdb.Queries
}

// NewRepository returns a Repository on pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, queries: sqlcdb.New(pool)}
}

// Ensure creates an empty persisted session when a board is opened before its
// first poll. Existing sessions are returned unchanged.
func (r *Repository) Ensure(ctx context.Context, id Identity) (Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin draft session initialization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureSession(ctx, r.queries.WithTx(tx), id); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit draft session initialization: %w", err)
	}
	return r.Get(ctx, id.LeagueKey)
}

// Reconcile applies one successful Yahoo poll inside a row-locked transaction.
func (r *Repository) Reconcile(ctx context.Context, id Identity, poll PollResult) (Session, draftsession.Report, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Session{}, draftsession.Report{}, fmt.Errorf("begin draft reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := r.queries.WithTx(tx)
	if err := ensureSession(ctx, queries, id); err != nil {
		return Session{}, draftsession.Report{}, err
	}
	current, err := loadStateForUpdate(ctx, queries, id.LeagueKey)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	next, report, err := draftsession.Reconcile(current, poll.Snapshot)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	stateRaw, boardHash, err := encodeState(next)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	version, err := draftsession.DatabaseVersion(next.Version)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	if err := updateSuccessfulSession(ctx, queries, id.LeagueKey, poll, next, version, report, stateRaw, boardHash); err != nil {
		return Session{}, draftsession.Report{}, err
	}
	if report.Changed {
		if err := insertEvent(ctx, queries, id.LeagueKey, version, "upstream_reconcile", report, poll.PolledAt); err != nil {
			return Session{}, draftsession.Report{}, err
		}
	}
	if err := insertObservation(ctx, queries, id.LeagueKey, successfulObservation(poll, report)); err != nil {
		return Session{}, draftsession.Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, draftsession.Report{}, fmt.Errorf("commit draft reconciliation: %w", err)
	}
	session, err := r.Get(ctx, id.LeagueKey)
	return session, report, err
}

// RecordFailure keeps the last usable board while recording poll and error state.
func (r *Repository) RecordFailure(ctx context.Context, id Identity, at time.Time, duration time.Duration, pollErr error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin draft failure record: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := r.queries.WithTx(tx)
	if err := ensureSession(ctx, queries, id); err != nil {
		return err
	}
	err = queries.UpdateDraftSessionFailure(ctx, sqlcdb.UpdateDraftSessionFailureParams{
		PolledAt: timestamptz(at), LastError: pollErr.Error(), LeagueKey: id.LeagueKey,
	})
	if err != nil {
		return fmt.Errorf("update failed draft poll: %w", err)
	}
	observation := Observation{
		PolledAt: at, Duration: nonNegativeDuration(duration),
		ErrorClass: ClassifyError(pollErr), Error: pollErr.Error(),
	}
	if err := insertObservation(ctx, queries, id.LeagueKey, observation); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit draft failure record: %w", err)
	}
	return nil
}

// ApplyManual records one local add, correction, or undo transactionally.
func (r *Repository) ApplyManual(ctx context.Context, id Identity, operation draftsession.ManualOperation, at time.Time) (Session, draftsession.Report, error) {
	return r.applyManual(ctx, id, operation, at, nil)
}

// ApplyManualAtVersion applies a manual operation only if the board still has
// the expected reducer version. The comparison is made under the session row
// lock in the same transaction as the state change.
func (r *Repository) ApplyManualAtVersion(ctx context.Context, id Identity, operation draftsession.ManualOperation, expectedVersion uint64, at time.Time) (Session, draftsession.Report, error) {
	return r.applyManual(ctx, id, operation, at, &expectedVersion)
}

func (r *Repository) applyManual(ctx context.Context, id Identity, operation draftsession.ManualOperation, at time.Time, expectedVersion *uint64) (Session, draftsession.Report, error) {
	kind := "manual_" + string(operation.Kind)
	return r.mutate(ctx, id, kind, at, expectedVersion, func(state draftsession.State) (draftsession.State, draftsession.Report, error) {
		return draftsession.ApplyManual(state, operation)
	})
}

// ResolveConflict applies the required explicit conflict choice.
func (r *Repository) ResolveConflict(ctx context.Context, id Identity, key draftsession.PickKey, choice draftsession.ConflictChoice, at time.Time) (Session, draftsession.Report, error) {
	return r.resolveConflict(ctx, id, key, choice, at, nil)
}

// ResolveConflictAtVersion resolves a conflict only if the board still has
// the expected reducer version.
func (r *Repository) ResolveConflictAtVersion(ctx context.Context, id Identity, key draftsession.PickKey, choice draftsession.ConflictChoice, expectedVersion uint64, at time.Time) (Session, draftsession.Report, error) {
	return r.resolveConflict(ctx, id, key, choice, at, &expectedVersion)
}

func (r *Repository) resolveConflict(ctx context.Context, id Identity, key draftsession.PickKey, choice draftsession.ConflictChoice, at time.Time, expectedVersion *uint64) (Session, draftsession.Report, error) {
	kind := "resolve_" + string(choice)
	return r.mutate(ctx, id, kind, at, expectedVersion, func(state draftsession.State) (draftsession.State, draftsession.Report, error) {
		return draftsession.ResolveConflict(state, key, choice)
	})
}

type stateMutation func(draftsession.State) (draftsession.State, draftsession.Report, error)

func (r *Repository) mutate(ctx context.Context, id Identity, kind string, at time.Time, expectedVersion *uint64, mutate stateMutation) (Session, draftsession.Report, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Session{}, draftsession.Report{}, fmt.Errorf("begin draft mutation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := r.queries.WithTx(tx)
	if err := ensureSession(ctx, queries, id); err != nil {
		return Session{}, draftsession.Report{}, err
	}
	current, err := loadStateForUpdate(ctx, queries, id.LeagueKey)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	if expectedVersion != nil && current.Version != *expectedVersion {
		return Session{}, draftsession.Report{}, &StaleStateVersionError{Expected: *expectedVersion, Actual: current.Version}
	}
	next, report, err := mutate(current)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	if !report.Changed {
		return Session{}, draftsession.Report{}, fmt.Errorf("draft mutation %s made no state change", kind)
	}
	raw, hash, err := encodeState(next)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	version, err := draftsession.DatabaseVersion(next.Version)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	err = queries.UpdateDraftSessionBoard(ctx, sqlcdb.UpdateDraftSessionBoardParams{
		StateVersion: version, Board: raw, BoardHash: hash, SafeToRecommend: report.SafeToRecommend,
		UpdatedAt: timestamptz(at), LeagueKey: id.LeagueKey,
	})
	if err != nil {
		return Session{}, draftsession.Report{}, fmt.Errorf("update manual draft state: %w", err)
	}
	if err := insertEvent(ctx, queries, id.LeagueKey, version, kind, report, at); err != nil {
		return Session{}, draftsession.Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, draftsession.Report{}, fmt.Errorf("commit draft mutation: %w", err)
	}
	session, err := r.Get(ctx, id.LeagueKey)
	return session, report, err
}

// Get returns one session and its reducer state.
func (r *Repository) Get(ctx context.Context, leagueKey string) (Session, error) {
	row, err := r.queries.GetDraftSession(ctx, leagueKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, fmt.Errorf("load draft session %s: %w", leagueKey, ErrSessionNotFound)
	}
	if err != nil {
		return Session{}, fmt.Errorf("load draft session %s: %w", leagueKey, err)
	}
	return sessionFromRow(leagueKey, row)
}

// ListEvents returns state-changing events strictly newer than afterVersion in
// ascending version order. The limit is bounded to protect reconnect polling.
func (r *Repository) ListEvents(ctx context.Context, leagueKey string, afterVersion uint64, limit int) ([]Event, error) {
	if limit <= 0 {
		return nil, errors.New("draft event limit must be positive")
	}
	if limit > maxDraftEventPageSize {
		limit = maxDraftEventPageSize
	}
	databaseAfterVersion, err := draftsession.DatabaseVersion(afterVersion)
	if err != nil {
		return nil, fmt.Errorf("draft event cursor: %w", err)
	}
	maxEvents, err := int4("event limit", limit)
	if err != nil {
		return nil, err
	}
	rows, err := r.queries.ListDraftSessionEvents(ctx, sqlcdb.ListDraftSessionEventsParams{
		LeagueKey: leagueKey, AfterVersion: databaseAfterVersion, MaxEvents: maxEvents,
	})
	if err != nil {
		return nil, fmt.Errorf("list draft session events: %w", err)
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

// ListObservations returns the newest bounded observation history.
func (r *Repository) ListObservations(ctx context.Context, leagueKey string, limit int) ([]Observation, error) {
	maxObservations, err := int4("observation limit", limit)
	if err != nil {
		return nil, err
	}
	rows, err := r.queries.ListDraftSessionObservations(ctx, sqlcdb.ListDraftSessionObservationsParams{
		LeagueKey: leagueKey, MaxObservations: maxObservations,
	})
	if err != nil {
		return nil, fmt.Errorf("list draft observations: %w", err)
	}
	observations := make([]Observation, 0, len(rows))
	for _, row := range rows {
		observations = append(observations, observationFromRow(row))
	}
	return observations, nil
}

func ensureSession(ctx context.Context, queries *sqlcdb.Queries, id Identity) error {
	params, err := ensureSessionParams(id)
	if err != nil {
		return fmt.Errorf("create draft session: %w", err)
	}
	if err := queries.EnsureDraftSession(ctx, params); err != nil {
		return fmt.Errorf("create draft session: %w", err)
	}
	stored, err := queries.GetDraftSessionIdentity(ctx, id.LeagueKey)
	if err != nil {
		return fmt.Errorf("verify draft session identity: %w", err)
	}
	if stored.Season != params.Season || stored.LeagueID != params.LeagueID || stored.GameKey != params.GameKey {
		return fmt.Errorf("draft session %s identity mismatch", id.LeagueKey)
	}
	return nil
}

func loadStateForUpdate(ctx context.Context, queries *sqlcdb.Queries, leagueKey string) (draftsession.State, error) {
	row, err := queries.LockDraftSessionBoard(ctx, leagueKey)
	if err != nil {
		return draftsession.State{}, fmt.Errorf("lock draft session: %w", err)
	}
	state, err := decodeState(row.Board)
	if err != nil {
		return draftsession.State{}, err
	}
	if row.StateVersion < 0 || state.Version != uint64(row.StateVersion) {
		return draftsession.State{}, fmt.Errorf("draft session %s version mismatch", leagueKey)
	}
	return state, nil
}

func updateSuccessfulSession(ctx context.Context, queries *sqlcdb.Queries, leagueKey string, poll PollResult,
	state draftsession.State, version int64, report draftsession.Report, raw []byte, hash string) error {
	upstream, err := int4("upstream pick count", len(state.Upstream))
	if err != nil {
		return fmt.Errorf("update successful draft session: %w", err)
	}
	skipped, err := int4("skipped pick count", len(report.Skipped))
	if err != nil {
		return fmt.Errorf("update successful draft session: %w", err)
	}
	err = queries.UpdateDraftSessionPoll(ctx, sqlcdb.UpdateDraftSessionPollParams{
		StateVersion: version, DraftStatus: poll.DraftStatus, Board: raw, BoardHash: hash,
		RecommendationsSafe: report.SafeToRecommend, Complete: poll.DraftComplete,
		UpstreamPickCount: upstream, SkippedPickCount: skipped,
		PolledAt: timestamptz(poll.PolledAt), Authoritative: report.Complete, LeagueKey: leagueKey,
	})
	if err != nil {
		return fmt.Errorf("update successful draft session: %w", err)
	}
	return nil
}

func successfulObservation(poll PollResult, report draftsession.Report) Observation {
	return Observation{
		PolledAt: poll.PolledAt, Duration: nonNegativeDuration(poll.Duration), Success: true,
		Authoritative: report.Complete, Changed: report.Changed, DraftStatus: poll.DraftStatus,
		DeclaredCount: poll.Snapshot.ExpectedCount, ParsedCount: poll.Snapshot.RawCount,
		SkippedCount: len(report.Skipped), SnapshotHash: poll.SnapshotHash,
	}
}

func insertObservation(ctx context.Context, queries *sqlcdb.Queries, leagueKey string, observation Observation) error {
	params, err := observationParams(leagueKey, observation)
	if err != nil {
		return fmt.Errorf("insert draft observation: %w", err)
	}
	if err := queries.InsertDraftSessionObservation(ctx, params); err != nil {
		return fmt.Errorf("insert draft observation: %w", err)
	}
	return nil
}

func insertEvent(ctx context.Context, queries *sqlcdb.Queries, leagueKey string, version int64, kind string, details any, at time.Time) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal draft event: %w", err)
	}
	err = queries.InsertDraftSessionEvent(ctx, sqlcdb.InsertDraftSessionEventParams{
		LeagueKey: leagueKey, StateVersion: version, Kind: kind, Details: raw, CreatedAt: timestamptz(at),
	})
	if err != nil {
		return fmt.Errorf("insert draft event: %w", err)
	}
	return nil
}

func nonNegativeDuration(duration time.Duration) time.Duration {
	if duration < 0 {
		return 0
	}
	return duration
}
