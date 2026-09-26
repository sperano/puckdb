package draftrank

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/newsadjust"
)

// ErrOverridesUnavailable means the Service was built without override
// storage.
var ErrOverridesUnavailable = errors.New("override storage is not configured")

// OverrideStore is where manager overrides live (newsadjust.Repository).
type OverrideStore interface {
	ListOverrides(ctx context.Context) ([]newsadjust.Override, error)
	CreateOverride(ctx context.Context, o newsadjust.Override) error
	ResetOverride(ctx context.Context, id string, at time.Time, reason string) error
}

// OverrideState is whether an override is in force now.
type OverrideState string

const (
	OverrideActive  OverrideState = "active"
	OverrideExpired OverrideState = "expired"
	OverrideReset   OverrideState = "reset"
)

// OverrideStatus is an override with its state now.
type OverrideStatus struct {
	newsadjust.Override
	State OverrideState `json:"state"`
}

// OverrideFilter selects overrides. LeagueKey also matches overrides that
// cover every league.
type OverrideFilter struct {
	LeagueKey       string
	PlayerKey       string
	IncludeInactive bool
}

// Overrides lists overrides, newest first.
func (s *Service) Overrides(ctx context.Context, filter OverrideFilter) ([]OverrideStatus, error) {
	if s.overrides == nil {
		return nil, ErrOverridesUnavailable
	}
	all, err := s.overrides.ListOverrides(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var out []OverrideStatus
	for _, o := range all {
		if filter.LeagueKey != "" && o.LeagueKey != "" && o.LeagueKey != filter.LeagueKey {
			continue
		}
		if filter.PlayerKey != "" && o.PlayerKey != filter.PlayerKey {
			continue
		}
		status := OverrideStatus{Override: o, State: OverrideStateAt(o, now)}
		if status.State == OverrideActive || filter.IncludeInactive {
			out = append(out, status)
		}
	}
	slices.SortFunc(out, func(a, b OverrideStatus) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	return out, nil
}

// OverrideStateAt is whether an override is in force at now.
func OverrideStateAt(o newsadjust.Override, now time.Time) OverrideState {
	switch {
	case !o.ResetAt.IsZero() && !o.ResetAt.After(now):
		return OverrideReset
	case !o.ExpiresAt.IsZero() && !o.ExpiresAt.After(now):
		return OverrideExpired
	}
	return OverrideActive
}

// CreateOverride stores a new override created now by createdBy. It takes
// effect at the league's next refresh; until then the served snapshot
// reports OVERRIDES_CHANGED.
func (s *Service) CreateOverride(ctx context.Context, o newsadjust.Override, createdBy string) (OverrideStatus, error) {
	if s.overrides == nil {
		return OverrideStatus{}, ErrOverridesUnavailable
	}
	o.ID = newsadjust.NewOverrideID()
	o.CreatedAt = s.now().UTC()
	o.CreatedBy = createdBy
	o.ResetAt, o.ResetReason = time.Time{}, ""
	if err := o.Validate(); err != nil {
		return OverrideStatus{}, fmt.Errorf("%w: %w", ErrInvalidQuery, err)
	}
	if err := s.overrides.CreateOverride(ctx, o); err != nil {
		return OverrideStatus{}, err
	}
	return OverrideStatus{Override: o, State: OverrideStateAt(o, o.CreatedAt)}, nil
}

// ResetOverride ends an override now, keeping its record.
func (s *Service) ResetOverride(ctx context.Context, id, reason string) error {
	if s.overrides == nil {
		return ErrOverridesUnavailable
	}
	return s.overrides.ResetOverride(ctx, id, s.now().UTC(), reason)
}
