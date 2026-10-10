package newsadjust

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ErrExclusionTarget means an exclusion does not name a stored news event
// about its player.
var ErrExclusionTarget = errors.New("exclusion must name a stored news event about its player")

// ErrExclusionUnsupported means an exclusion names a stored news event
// whose effect on other events it cannot undo.
var ErrExclusionUnsupported = errors.New("exclusion cannot undo this event's effect on other events")

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
	if o.Kind == OverrideExcludeEvent {
		if err := r.checkExclusionTarget(ctx, o); err != nil {
			return err
		}
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

// checkExclusionTarget refuses an exclusion unless its event is a stored
// news event about the override's player, under the same NHL and Yahoo
// identities the adjustment maps events to players with. It also refuses
// an event whose effect on other events extraction stores on those events
// (the lifecycle of what it superseded or resolved): excluding it could not
// undo that effect.
func (r *Repository) checkExclusionTarget(ctx context.Context, o Override) error {
	digits, stored := strings.CutPrefix(o.EventID, extractedIDPrefix)
	eventID, err := strconv.ParseInt(digits, 10, 64)
	if !stored || err != nil || eventID <= 0 {
		return fmt.Errorf("override %s: event %q is not a stored news event: %w", o.ID, o.EventID, ErrExclusionTarget)
	}
	params := sqlcdb.GetNewsEventExclusionTargetParams{EventID: eventID, ReinstatementType: string(newsevent.TypeReinstatement)}
	if id, isNHL := nhlPlayerID(o.PlayerKey); isNHL {
		params.NhlPlayerID = pgtype.Int8{Int64: id, Valid: true}
	} else if id, isYahoo := yahooPlayerID(o.PlayerKey); isYahoo && id <= math.MaxInt32 {
		params.YahooPlayerID = pgtype.Int4{Int32: int32(id), Valid: true}
	} else {
		return fmt.Errorf("override %s: player key %q names neither an NHL nor a Yahoo player: %w", o.ID, o.PlayerKey, ErrExclusionTarget)
	}
	target, err := r.queries.GetNewsEventExclusionTarget(ctx, params)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("override %s: news event %s does not exist: %w", o.ID, o.EventID, ErrExclusionTarget)
	case err != nil:
		return fmt.Errorf("override %s: check event %s: %w", o.ID, o.EventID, err)
	case !target.AboutPlayer:
		return fmt.Errorf("override %s: news event %s is not about %s: %w", o.ID, o.EventID, o.PlayerKey, ErrExclusionTarget)
	case target.IsReinstatement:
		return fmt.Errorf("override %s: news event %s is a reinstatement, whose resolution is stored on the absences it ends; "+
			"set a missed_games override instead: %w", o.ID, o.EventID, ErrExclusionUnsupported)
	case target.SupersedesAnother:
		return fmt.Errorf("override %s: news event %s superseded another event, which stays superseded; "+
			"set a missed_games or input override instead: %w", o.ID, o.EventID, ErrExclusionUnsupported)
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
