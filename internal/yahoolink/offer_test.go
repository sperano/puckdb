package yahoolink

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResetTeamOffer: a reset brings the offer back for the user alone and
// keeps the user's selection; resetting a pending offer reports it was not
// seen.
func TestResetTeamOffer(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPGStore(pool)
	ctx := context.Background()

	league, team := seedLeagueTeam(t, pool, "Picked")
	user := insertUser(t, pool, "reset")
	other := insertUser(t, pool, "keep")
	picker := newTestPicker(pool, testSeason, league)
	_, err := picker.SetTeams(ctx, userIDText(user), []TeamRef{{league, team}})
	require.NoError(t, err)
	require.NoError(t, picker.DismissTeamOffer(ctx, userIDText(other)))

	wasSeen, err := store.ResetTeamOffer(ctx, user)
	require.NoError(t, err)
	assert.True(t, wasSeen)
	choices, err := picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.False(t, choices.Offered)
	assert.Equal(t, []string{state(league, team, SourceSelf)}, choiceState(choices), "the selection stays")

	otherChoices, err := picker.TeamChoices(ctx, userIDText(other))
	require.NoError(t, err)
	assert.True(t, otherChoices.Offered, "another user's offer stays seen")

	wasSeen, err = store.ResetTeamOffer(ctx, user)
	require.NoError(t, err)
	assert.False(t, wasSeen, "a pending offer was not seen")
}

func TestResetTeamOffer_UnknownUser(t *testing.T) {
	pool := pgtest.Open(t)
	missing := pgtype.UUID{Bytes: [16]byte{0xde, 0xad}, Valid: true}

	_, err := NewPGStore(pool).ResetTeamOffer(context.Background(), missing)
	require.ErrorIs(t, err, ErrUserNotFound)
	assert.EqualError(t, err, "no such user: id "+missing.String())
}

// TestResetInstanceTeamOffers: every user of the instance who saw the offer
// is reset and counted, a pending one is not counted, and another
// instance's user keeps the offer seen.
func TestResetInstanceTeamOffers(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPGStore(pool)
	ctx := context.Background()

	// A fresh instance: the test database is shared and never truncated.
	instance := uniqueUsername("instance")
	league, _ := seedLeagueTeam(t, pool, "Team")
	picker := newTestPicker(pool, testSeason, league)
	seen1 := insertUserOnInstance(t, pool, instance, "seen1")
	seen2 := insertUserOnInstance(t, pool, instance, "seen2")
	pending := insertUserOnInstance(t, pool, instance, "pending")
	elsewhere := insertUserOnInstance(t, pool, uniqueUsername("elsewhere"), "seen")
	for _, u := range []pgtype.UUID{seen1, seen2, elsewhere} {
		require.NoError(t, picker.DismissTeamOffer(ctx, userIDText(u)))
	}

	n, err := store.ResetInstanceTeamOffers(ctx, instance)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	for _, u := range []pgtype.UUID{seen1, seen2, pending} {
		choices, err := picker.TeamChoices(ctx, userIDText(u))
		require.NoError(t, err)
		assert.False(t, choices.Offered)
	}
	choices, err := picker.TeamChoices(ctx, userIDText(elsewhere))
	require.NoError(t, err)
	assert.True(t, choices.Offered, "another instance's user keeps the offer seen")

	n, err = store.ResetInstanceTeamOffers(ctx, instance)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "nothing left to reset")
}

// TestResetUserTeamOffer: the named user of the instance is reset alone; a
// name no user of the instance carries, or two users share, resets nobody.
func TestResetUserTeamOffer(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPGStore(pool)
	ctx := context.Background()

	instance := uniqueUsername("instance")
	league, _ := seedLeagueTeam(t, pool, "Team")
	picker := newTestPicker(pool, testSeason, league)
	alice := insertUserOnInstance(t, pool, instance, "alice")
	bob := insertUserOnInstance(t, pool, instance, "bob")
	twin1 := insertUserOnInstance(t, pool, instance, "twin")
	twin2 := insertUserOnInstance(t, pool, instance, "twin")
	elsewhere := insertUserOnInstance(t, pool, uniqueUsername("elsewhere"), "carol")
	for _, u := range []pgtype.UUID{alice, bob, twin1, twin2, elsewhere} {
		require.NoError(t, picker.DismissTeamOffer(ctx, userIDText(u)))
	}

	wasSeen, err := store.ResetUserTeamOffer(ctx, instance, "alice")
	require.NoError(t, err)
	assert.True(t, wasSeen)
	wasSeen, err = store.ResetUserTeamOffer(ctx, instance, "alice")
	require.NoError(t, err)
	assert.False(t, wasSeen, "a pending offer was not seen")

	_, err = store.ResetUserTeamOffer(ctx, instance, "carol")
	require.ErrorIs(t, err, ErrUserNotFound, "another instance's user is not found")
	assert.EqualError(t, err, `no such user: "carol" on instance "`+instance+`"`)
	_, err = store.ResetUserTeamOffer(ctx, instance, "twin")
	require.ErrorIs(t, err, ErrAmbiguousUser)

	for _, u := range []pgtype.UUID{bob, twin1, twin2, elsewhere} {
		choices, err := picker.TeamChoices(ctx, userIDText(u))
		require.NoError(t, err)
		assert.True(t, choices.Offered, "only the named user is reset")
	}
}
