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
)

const maxDraftEventPageSize = 200

var (
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
type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Ensure creates an empty persisted session when a board is opened before its
// first poll. Existing sessions are returned unchanged.
func (r *Repository) Ensure(ctx context.Context, id Identity) (Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin draft session initialization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureSession(ctx, tx, id); err != nil {
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
	if err := ensureSession(ctx, tx, id); err != nil {
		return Session{}, draftsession.Report{}, err
	}
	current, err := loadStateForUpdate(ctx, tx, id.LeagueKey)
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
	if err := updateSuccessfulSession(ctx, tx, id.LeagueKey, poll, next, report, stateRaw, boardHash); err != nil {
		return Session{}, draftsession.Report{}, err
	}
	if report.Changed {
		if err := insertEvent(ctx, tx, id.LeagueKey, databaseVersion(next.Version), "upstream_reconcile", report, poll.PolledAt); err != nil {
			return Session{}, draftsession.Report{}, err
		}
	}
	if err := insertObservation(ctx, tx, id.LeagueKey, successfulObservation(poll, report)); err != nil {
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
	if err := ensureSession(ctx, tx, id); err != nil {
		return err
	}
	const update = `
UPDATE draft_sessions SET last_poll_at=$2, last_error=$3,
    recommendations_safe=false, sync_version=sync_version+1, updated_at=$2 WHERE league_key=$1`
	if _, err := tx.Exec(ctx, update, id.LeagueKey, at, pollErr.Error()); err != nil {
		return fmt.Errorf("update failed draft poll: %w", err)
	}
	observation := Observation{
		PolledAt: at, Duration: nonNegativeDuration(duration),
		ErrorClass: ClassifyError(pollErr), Error: pollErr.Error(),
	}
	if err := insertObservation(ctx, tx, id.LeagueKey, observation); err != nil {
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
	if err := ensureSession(ctx, tx, id); err != nil {
		return Session{}, draftsession.Report{}, err
	}
	current, err := loadStateForUpdate(ctx, tx, id.LeagueKey)
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
	const update = `
UPDATE draft_sessions SET state_version=$2, board=$3, board_hash=$4,
	recommendations_safe=($5 AND last_error=''), sync_version=sync_version+1, updated_at=$6 WHERE league_key=$1`
	if _, err := tx.Exec(ctx, update, id.LeagueKey, databaseVersion(next.Version), raw, hash, report.SafeToRecommend, at); err != nil {
		return Session{}, draftsession.Report{}, fmt.Errorf("update manual draft state: %w", err)
	}
	if err := insertEvent(ctx, tx, id.LeagueKey, databaseVersion(next.Version), kind, report, at); err != nil {
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
	const query = `
SELECT season, league_id, game_key, board, state_version, draft_status,
       recommendations_safe, complete, upstream_pick_count, skipped_pick_count,
       last_poll_at, last_success_at, last_authoritative_at, last_error,
       sync_version, updated_at
FROM draft_sessions WHERE league_key=$1`
	var session Session
	var board []byte
	var version, syncVersion int64
	err := r.pool.QueryRow(ctx, query, leagueKey).Scan(
		&session.Identity.Season, &session.Identity.LeagueID, &session.Identity.GameKey,
		&board, &version, &session.DraftStatus, &session.RecommendationsSafe,
		&session.Complete, &session.UpstreamPickCount, &session.SkippedPickCount,
		&session.LastPollAt, &session.LastSuccessAt, &session.LastAuthoritativeAt, &session.LastError,
		&syncVersion, &session.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, fmt.Errorf("load draft session %s: %w", leagueKey, ErrSessionNotFound)
	}
	if err != nil {
		return Session{}, fmt.Errorf("load draft session %s: %w", leagueKey, err)
	}
	session.Identity.LeagueKey = leagueKey
	session.State, err = decodeState(board)
	if err != nil {
		return Session{}, err
	}
	if version < 0 || session.State.Version != uint64(version) {
		return Session{}, fmt.Errorf("draft session %s version mismatch: row=%d board=%d", leagueKey, version, session.State.Version)
	}
	if syncVersion < 0 {
		return Session{}, fmt.Errorf("draft session %s has invalid sync version %d", leagueKey, syncVersion)
	}
	session.SyncVersion = uint64(syncVersion)
	return session, nil
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
	if afterVersion > uint64(^uint64(0)>>1) {
		return nil, errors.New("draft event cursor exceeds PostgreSQL bigint")
	}
	const query = `
SELECT state_version, kind, details, created_at
FROM draft_session_events
WHERE league_key=$1 AND state_version>$2
ORDER BY state_version ASC
LIMIT $3`
	rows, err := r.pool.Query(ctx, query, leagueKey, int64(afterVersion), limit)
	if err != nil {
		return nil, fmt.Errorf("list draft session events: %w", err)
	}
	defer rows.Close()
	events := make([]Event, 0, limit)
	for rows.Next() {
		var event Event
		var version int64
		if err := rows.Scan(&version, &event.Kind, &event.Details, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan draft session event: %w", err)
		}
		if version <= 0 {
			return nil, fmt.Errorf("draft session event has invalid version %d", version)
		}
		event.StateVersion = uint64(version)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read draft session events: %w", err)
	}
	return events, nil
}

// ListObservations returns the newest bounded observation history.
func (r *Repository) ListObservations(ctx context.Context, leagueKey string, limit int) ([]Observation, error) {
	const query = `
SELECT polled_at, duration_ms, success, authoritative, changed, draft_status,
       declared_count, parsed_count, skipped_count, snapshot_hash, error_class, error
FROM draft_session_observations WHERE league_key=$1
ORDER BY polled_at DESC, id DESC LIMIT $2`
	rows, err := r.pool.Query(ctx, query, leagueKey, limit)
	if err != nil {
		return nil, fmt.Errorf("list draft observations: %w", err)
	}
	defer rows.Close()
	var observations []Observation
	for rows.Next() {
		var observation Observation
		var durationMillis int64
		if err := rows.Scan(
			&observation.PolledAt, &durationMillis, &observation.Success, &observation.Authoritative,
			&observation.Changed, &observation.DraftStatus, &observation.DeclaredCount,
			&observation.ParsedCount, &observation.SkippedCount, &observation.SnapshotHash,
			&observation.ErrorClass, &observation.Error,
		); err != nil {
			return nil, fmt.Errorf("scan draft observation: %w", err)
		}
		observation.Duration = time.Duration(durationMillis) * time.Millisecond
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

func ensureSession(ctx context.Context, tx pgx.Tx, id Identity) error {
	const insert = `
INSERT INTO draft_sessions (league_key, season, league_id, game_key)
VALUES ($1,$2,$3,$4) ON CONFLICT (league_key) DO NOTHING`
	if _, err := tx.Exec(ctx, insert, id.LeagueKey, id.Season, id.LeagueID, id.GameKey); err != nil {
		return fmt.Errorf("create draft session: %w", err)
	}
	const verify = `SELECT season, league_id, game_key FROM draft_sessions WHERE league_key=$1`
	var season, leagueID, gameKey int
	if err := tx.QueryRow(ctx, verify, id.LeagueKey).Scan(&season, &leagueID, &gameKey); err != nil {
		return fmt.Errorf("verify draft session identity: %w", err)
	}
	if season != id.Season || leagueID != id.LeagueID || gameKey != id.GameKey {
		return fmt.Errorf("draft session %s identity mismatch", id.LeagueKey)
	}
	return nil
}

func loadStateForUpdate(ctx context.Context, tx pgx.Tx, leagueKey string) (draftsession.State, error) {
	const query = `SELECT board, state_version FROM draft_sessions WHERE league_key=$1 FOR UPDATE`
	var raw []byte
	var version int64
	if err := tx.QueryRow(ctx, query, leagueKey).Scan(&raw, &version); err != nil {
		return draftsession.State{}, fmt.Errorf("lock draft session: %w", err)
	}
	state, err := decodeState(raw)
	if err != nil {
		return draftsession.State{}, err
	}
	if version < 0 || state.Version != uint64(version) {
		return draftsession.State{}, fmt.Errorf("draft session %s version mismatch", leagueKey)
	}
	return state, nil
}

func updateSuccessfulSession(ctx context.Context, tx pgx.Tx, leagueKey string, poll PollResult,
	state draftsession.State, report draftsession.Report, raw []byte, hash string) error {
	const update = `
UPDATE draft_sessions SET state_version=$2, draft_status=$3, board=$4, board_hash=$5,
    recommendations_safe=$6, complete=$7, upstream_pick_count=$8, skipped_pick_count=$9,
    last_poll_at=$10, last_success_at=$10,
    last_authoritative_at=CASE WHEN $11 THEN $10 ELSE last_authoritative_at END,
    last_error='', sync_version=sync_version+1, updated_at=$10
WHERE league_key=$1`
	_, err := tx.Exec(ctx, update, leagueKey, databaseVersion(state.Version), poll.DraftStatus, raw, hash,
		report.SafeToRecommend, poll.DraftComplete, len(state.Upstream), len(report.Skipped),
		poll.PolledAt, report.Complete)
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

func insertObservation(ctx context.Context, tx pgx.Tx, leagueKey string, observation Observation) error {
	const insert = `
INSERT INTO draft_session_observations (
    league_key, polled_at, duration_ms, success, authoritative, changed, draft_status,
    declared_count, parsed_count, skipped_count, snapshot_hash, error_class, error
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	_, err := tx.Exec(ctx, insert, leagueKey, observation.PolledAt, observation.Duration.Milliseconds(),
		observation.Success, observation.Authoritative, observation.Changed, observation.DraftStatus,
		observation.DeclaredCount, observation.ParsedCount, observation.SkippedCount,
		observation.SnapshotHash, observation.ErrorClass, observation.Error)
	if err != nil {
		return fmt.Errorf("insert draft observation: %w", err)
	}
	return nil
}

func insertEvent(ctx context.Context, tx pgx.Tx, leagueKey string, version int64, kind string, details any, at time.Time) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal draft event: %w", err)
	}
	const insert = `
INSERT INTO draft_session_events (league_key, state_version, kind, details, created_at)
VALUES ($1,$2,$3,$4,$5)`
	if _, err := tx.Exec(ctx, insert, leagueKey, version, kind, raw, at); err != nil {
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

func databaseVersion(version uint64) int64 {
	const maxDatabaseVersion = uint64(^uint64(0) >> 1)
	if version > maxDatabaseVersion {
		panic("draft session version exceeds PostgreSQL bigint")
	}
	return int64(version)
}
