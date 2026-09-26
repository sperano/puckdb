package newsadjust

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ErrOverrideNotResettable means the override does not exist, was already
// reset, or the reset time precedes its creation.
var ErrOverrideNotResettable = errors.New("override not found, already reset, or reset before it was created")

// Repository stores overrides and adjustment runs.
type Repository struct {
	pool        *pgxpool.Pool
	queries     *sqlcdb.Queries
	projections *projection.Repository
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, queries: sqlcdb.New(pool), projections: projection.NewRepository(pool)}
}

// NewOverrideID returns a fresh override ID.
func NewOverrideID() string {
	return uuid.NewString()
}

// CreateOverride stores a new override. A reset is recorded later with
// ResetOverride, never at creation.
func (r *Repository) CreateOverride(ctx context.Context, o Override) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if !o.ResetAt.IsZero() {
		return fmt.Errorf("override %s: record resets with ResetOverride", o.ID)
	}
	err := r.queries.CreateNewsAdjustmentOverride(ctx, sqlcdb.CreateNewsAdjustmentOverrideParams{
		ID: o.ID, PlayerKey: o.PlayerKey, LeagueKey: o.LeagueKey, Kind: string(o.Kind),
		EventID: o.EventID, Scenario: string(o.Scenario), Input: string(o.Input), Value: o.Value,
		Reason: o.Reason, CreatedBy: o.CreatedBy, CreatedAt: timestamp(o.CreatedAt), ExpiresAt: timestamp(o.ExpiresAt),
	})
	if err != nil {
		return fmt.Errorf("create override %s: %w", o.ID, err)
	}
	return nil
}

// ResetOverride ends an override at a time, keeping its record and values.
func (r *Repository) ResetOverride(ctx context.Context, id string, at time.Time, reason string) error {
	if strings.TrimSpace(reason) == "" || at.IsZero() {
		return fmt.Errorf("resetting override %s needs a time and a reason", id)
	}
	rows, err := r.queries.ResetNewsAdjustmentOverride(ctx, sqlcdb.ResetNewsAdjustmentOverrideParams{
		ID: id, ResetAt: timestamp(at), ResetReason: reason,
	})
	if err != nil {
		return fmt.Errorf("reset override %s: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("reset override %s: %w", id, ErrOverrideNotResettable)
	}
	return nil
}

// ListOverrides returns every override ever recorded, reset or not, so
// Apply can select what was in force at any as-of time.
func (r *Repository) ListOverrides(ctx context.Context) ([]Override, error) {
	rows, err := r.queries.ListNewsAdjustmentOverrides(ctx)
	if err != nil {
		return nil, fmt.Errorf("list overrides: %w", err)
	}
	overrides := make([]Override, 0, len(rows))
	for _, row := range rows {
		overrides = append(overrides, overrideFromRow(row))
	}
	return overrides, nil
}

func overrideFromRow(row sqlcdb.NewsAdjustmentOverride) Override {
	return Override{
		ID: row.ID, PlayerKey: row.PlayerKey, LeagueKey: row.LeagueKey, Kind: OverrideKind(row.Kind),
		EventID: row.EventID, Scenario: Scenario(row.Scenario), Input: Input(row.Input), Value: row.Value,
		Reason: row.Reason, CreatedBy: row.CreatedBy, CreatedAt: timeOf(row.CreatedAt),
		ExpiresAt: timeOf(row.ExpiresAt), ResetAt: timeOf(row.ResetAt), ResetReason: row.ResetReason,
	}
}

func timestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: !t.IsZero()}
}

func timeOf(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

func uuidValue(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
