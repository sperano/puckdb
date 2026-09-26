package draftrank_test

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_OverridesLifecycle(t *testing.T) {
	store := draftfixtures.NewStore()
	svc := newService(store, serviceNow)
	ctx := context.Background()

	created, err := svc.CreateOverride(ctx, newsadjust.Override{
		PlayerKey: draftfixtures.SuspendedKey, LeagueKey: draftfixtures.LeagueKey, Kind: newsadjust.OverrideMissedGames,
		Value: 10, Reason: "league appeal reduced the suspension",
	}, "eric")
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "eric", created.CreatedBy)
	assert.Equal(t, serviceNow.UTC(), created.CreatedAt)
	assert.Equal(t, draftrank.OverrideActive, created.State)

	store.Overrides = append(store.Overrides,
		newsadjust.Override{ID: "other-league", PlayerKey: draftfixtures.TopCenter, LeagueKey: draftfixtures.OtherKey, CreatedAt: serviceNow},
		newsadjust.Override{ID: "expired", PlayerKey: draftfixtures.TopCenter, CreatedAt: draftfixtures.AsOf.Add(-time.Hour), ExpiresAt: draftfixtures.AsOf},
	)
	active, err := svc.Overrides(ctx, draftrank.OverrideFilter{LeagueKey: draftfixtures.LeagueKey})
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, created.ID, active[0].ID)

	all, err := svc.Overrides(ctx, draftrank.OverrideFilter{LeagueKey: draftfixtures.LeagueKey, IncludeInactive: true})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, draftrank.OverrideExpired, all[1].State)

	require.NoError(t, svc.ResetOverride(ctx, created.ID, "appeal lost"))
	after, err := svc.Overrides(ctx, draftrank.OverrideFilter{PlayerKey: draftfixtures.SuspendedKey, IncludeInactive: true})
	require.NoError(t, err)
	assert.Equal(t, draftrank.OverrideReset, after[0].State)
}

func TestService_CreateOverrideValidates(t *testing.T) {
	svc := newService(draftfixtures.NewStore(), serviceNow)
	_, err := svc.CreateOverride(context.Background(), newsadjust.Override{PlayerKey: draftfixtures.TopCenter, Kind: newsadjust.OverrideMissedGames}, "")
	assert.ErrorIs(t, err, draftrank.ErrInvalidQuery, "an override needs a reason")
}

func TestService_WithoutOverrideStore(t *testing.T) {
	svc := draftrank.NewService(draftfixtures.NewStore(), nil, draftrank.ServiceOptions{})
	_, err := svc.Overrides(context.Background(), draftrank.OverrideFilter{})
	assert.ErrorIs(t, err, draftrank.ErrOverridesUnavailable)
	_, err = svc.CreateOverride(context.Background(), newsadjust.Override{}, "")
	assert.ErrorIs(t, err, draftrank.ErrOverridesUnavailable)
	assert.ErrorIs(t, svc.ResetOverride(context.Background(), "x", "y"), draftrank.ErrOverridesUnavailable)
}
