package appuser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// PGStore keeps users in app_users and sessions in app_sessions.
type PGStore struct {
	pool        *pgxpool.Pool
	idleTimeout time.Duration
}

// NewPGStore returns a store whose sessions end after idleTimeout without a
// resolved request.
func NewPGStore(pool *pgxpool.Pool, idleTimeout time.Duration) *PGStore {
	return &PGStore{pool: pool, idleTimeout: idleTimeout}
}

// Resolve upserts the user, then in the same transaction (the upsert holds the
// user row lock, so concurrent requests of one user agree on one session):
//  1. continues the session named by the presented cookie, if it is open and
//     not idle;
//  2. otherwise ends the user's idle sessions and continues their most recent
//     active one, so clients that do not keep cookies (server-to-server
//     callers) still count as one session per stretch of use;
//  3. otherwise opens a session under FreshHash.
func (s *PGStore) Resolve(ctx context.Context, req SessionRequest) (Resolution, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Resolution{}, fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a safe no-op after a successful Commit (documented in pgx).
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlcdb.New(tx)

	user, err := q.UpsertAppUser(ctx, sqlcdb.UpsertAppUserParams{
		AuthProvider: ProviderAuthentik,
		AuthSubject:  req.Identity.Subject,
		Username:     req.Identity.Username,
		DisplayName:  pgtype.Text{String: req.Identity.DisplayName, Valid: req.Identity.DisplayName != ""},
	})
	if err != nil {
		return Resolution{}, fmt.Errorf("upsert user: %w", err)
	}
	if user.DisabledAt.Valid {
		return Resolution{}, ErrUserDisabled
	}
	session, created, err := s.resolveSession(ctx, q, user.ID, req)
	if err != nil {
		return Resolution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Resolution{}, fmt.Errorf("commit tx: %w", err)
	}
	return Resolution{User: User{ID: uuidString(user.ID), SessionID: uuidString(session)}, Created: created}, nil
}

func (s *PGStore) resolveSession(ctx context.Context, q *sqlcdb.Queries, user pgtype.UUID, req SessionRequest) (pgtype.UUID, bool, error) {
	idle := s.idleTimeout.Seconds()
	if req.PresentedHash != nil {
		id, err := q.TouchSessionByHash(ctx, sqlcdb.TouchSessionByHashParams{
			SessionKeyHash: req.PresentedHash, UserID: user, IdleSeconds: idle,
		})
		if err == nil {
			return id, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, false, fmt.Errorf("touch session: %w", err)
		}
	}
	if err := q.EndIdleSessions(ctx, sqlcdb.EndIdleSessionsParams{UserID: user, IdleSeconds: idle}); err != nil {
		return pgtype.UUID{}, false, fmt.Errorf("end idle sessions: %w", err)
	}
	id, err := q.TouchLatestActiveSession(ctx, sqlcdb.TouchLatestActiveSessionParams{UserID: user, IdleSeconds: idle})
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, false, fmt.Errorf("touch latest session: %w", err)
	}
	id, err = q.CreateAppSession(ctx, sqlcdb.CreateAppSessionParams{UserID: user, SessionKeyHash: req.FreshHash})
	if err != nil {
		return pgtype.UUID{}, false, fmt.Errorf("create session: %w", err)
	}
	return id, true, nil
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
