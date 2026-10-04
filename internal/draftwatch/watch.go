package draftwatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draftsession"
)

// Source supplies one force-refreshed Yahoo observation.
type Source interface {
	Poll(context.Context, Identity) (PollResult, error)
}

// SessionRepository is the persistence surface used by a runner.
type SessionRepository interface {
	Reconcile(context.Context, Identity, PollResult) (Session, draftsession.Report, error)
	RecordFailure(context.Context, Identity, time.Time, time.Duration, error) error
}

// Outcome reports one watch iteration.
type Outcome struct {
	Session Session
	Report  draftsession.Report
	Err     error
	Final   bool
}

// WatchOptions controls cadence and graceful final reconciliation.
type WatchOptions struct {
	Interval     time.Duration
	MaxBackoff   time.Duration
	FinalTimeout time.Duration
	OnOutcome    func(Outcome)
}

// Runner coordinates fetch, transactional reconciliation, persistence and
// the database-wide per-session synchronization lock.
type Runner struct {
	Pool       *pgxpool.Pool
	Repository SessionRepository
	Source     Source
	Now        func() time.Time
}

// SyncOnce performs one targeted poll and records success or failure. It uses
// the same session lock as Watch so a one-shot poll cannot reconcile an older
// observation over an active watch iteration.
func (r Runner) SyncOnce(ctx context.Context, id Identity) (Session, draftsession.Report, error) {
	lock, err := acquireSessionSync(ctx, r.Pool, id.LeagueKey)
	if err != nil {
		return Session{}, draftsession.Report{}, err
	}
	defer lock.Close()
	return r.syncOnce(ctx, id)
}

func (r Runner) syncOnce(ctx context.Context, id Identity) (Session, draftsession.Report, error) {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	started := now().UTC()
	poll, err := r.Source.Poll(ctx, id)
	if err != nil {
		finished := now().UTC()
		recordErr := r.Repository.RecordFailure(ctx, id, finished, finished.Sub(started), err)
		if recordErr != nil {
			return Session{}, draftsession.Report{}, errors.Join(err, recordErr)
		}
		return Session{}, draftsession.Report{}, err
	}
	return r.Repository.Reconcile(ctx, id, poll)
}

// Watch polls until cancellation or Yahoo reports postdraft. Cancellation
// gets one bounded, detached final reconciliation before the session lock is
// released.
func (r Runner) Watch(ctx context.Context, id Identity, options WatchOptions) error {
	if err := validateWatchOptions(options); err != nil {
		return err
	}
	syncLock, err := acquireSessionSync(ctx, r.Pool, id.LeagueKey)
	if err != nil {
		return err
	}
	defer syncLock.Close()
	backoff := options.Interval
	for {
		session, report, pollErr := r.syncOnce(ctx, id)
		notifyOutcome(options.OnOutcome, Outcome{Session: session, Report: report, Err: pollErr})
		if pollErr == nil {
			backoff = options.Interval
			if session.Complete {
				return nil
			}
		} else if ErrorClass(pollErr) == "rate_limited" {
			// GenericClient does not retain Retry-After yet. Use the configured
			// ceiling rather than repeatedly probing Yahoo after an explicit 429.
			backoff = options.MaxBackoff
		} else {
			backoff = nextBackoff(backoff, options.Interval, options.MaxBackoff)
		}
		if err := waitForNextPoll(ctx, backoff); err != nil {
			return r.finalReconcile(id, options, err)
		}
	}
}

func (r Runner) finalReconcile(id Identity, options WatchOptions, cancellation error) error {
	ctx, cancel := context.WithTimeout(context.Background(), options.FinalTimeout)
	defer cancel()
	session, report, err := r.syncOnce(ctx, id)
	notifyOutcome(options.OnOutcome, Outcome{Session: session, Report: report, Err: err, Final: true})
	if err != nil {
		return errors.Join(cancellation, fmt.Errorf("final draft reconciliation: %w", err))
	}
	return cancellation
}

func validateWatchOptions(options WatchOptions) error {
	if options.Interval <= 0 {
		return fmt.Errorf("draft poll interval must be positive")
	}
	if options.MaxBackoff < options.Interval {
		return fmt.Errorf("draft maximum backoff must be at least the poll interval")
	}
	if options.FinalTimeout <= 0 {
		return fmt.Errorf("draft final timeout must be positive")
	}
	return nil
}

func nextBackoff(current, interval, maximum time.Duration) time.Duration {
	if current < interval {
		current = interval
	}
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func waitForNextPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func notifyOutcome(notify func(Outcome), outcome Outcome) {
	if notify != nil {
		notify(outcome)
	}
}

type sessionSyncLock struct {
	conn      *pgxpool.Conn
	leagueKey string
}

func acquireSessionSync(ctx context.Context, pool *pgxpool.Pool, leagueKey string) (*sessionSyncLock, error) {
	if pool == nil {
		return nil, fmt.Errorf("draft synchronization database pool is required")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire draft synchronization connection: %w", err)
	}
	const query = `SELECT pg_try_advisory_lock(hashtextextended('draft-session-sync:' || $1, 0))`
	var acquired bool
	if err := conn.QueryRow(ctx, query, leagueKey).Scan(&acquired); err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquire draft synchronization lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, fmt.Errorf("draft synchronization is already running for %s", leagueKey)
	}
	return &sessionSyncLock{conn: conn, leagueKey: leagueKey}, nil
}

func (l *sessionSyncLock) Close() {
	if l == nil || l.conn == nil {
		return
	}
	const query = `SELECT pg_advisory_unlock(hashtextextended('draft-session-sync:' || $1, 0))`
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var unlocked bool
	err := l.conn.QueryRow(ctx, query, l.leagueKey).Scan(&unlocked)
	if err != nil || !unlocked {
		// A session advisory lock survives transaction boundaries. If release
		// is uncertain, remove the connection from the pool and close the
		// underlying PostgreSQL session so it cannot retain or reenter the lock.
		connection := l.conn.Hijack()
		l.conn = nil
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = connection.Close(closeCtx)
		return
	}
	l.conn.Release()
	l.conn = nil
}
