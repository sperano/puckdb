package cmd

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/yahoolink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetTeamOfferTarget(t *testing.T) {
	username, err := resetTeamOfferTarget([]string{"alice"}, false)
	require.NoError(t, err)
	assert.Equal(t, "alice", username)

	username, err = resetTeamOfferTarget(nil, true)
	require.NoError(t, err)
	assert.Empty(t, username)

	_, err = resetTeamOfferTarget(nil, false)
	require.ErrorIs(t, err, errResetTeamOfferTarget)
	assert.EqualError(t, err, "name one user or pass --all-users, not both")
	_, err = resetTeamOfferTarget([]string{"alice"}, true)
	require.ErrorIs(t, err, errResetTeamOfferTarget)

	_, err = resetTeamOfferTarget([]string{""}, false)
	require.ErrorIs(t, err, errEmptyUsername, "an empty username never means every user")
}

// insertResetOfferUser inserts a user of instance whose team offer was
// seen when seen is set.
func insertResetOfferUser(t *testing.T, pool *pgxpool.Pool, instance, username string, seen bool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO app_users (instance, auth_provider, auth_subject, username, team_offer_seen_at)
		 VALUES ($1, 'authentik', $2, $3, CASE WHEN $4::boolean THEN now() END) RETURNING id`,
		instance, fmt.Sprintf("sub-%d", newMauriceTeamsLeagueID()), username, seen).Scan(&id))
	return id
}

// teamOfferSeen reports whether user's team offer flag is set.
func teamOfferSeen(t *testing.T, pool *pgxpool.Pool, user pgtype.UUID) bool {
	t.Helper()
	var seen bool
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT team_offer_seen_at IS NOT NULL FROM app_users WHERE id = $1`, user).Scan(&seen))
	return seen
}

// newResetOfferInstance returns an instance name no earlier test run used:
// the test database is shared and never truncated.
func newResetOfferInstance() string { return fmt.Sprintf("reset-offer-%d", newMauriceTeamsLeagueID()) }

func TestResetTeamOffer_OneUser(t *testing.T) {
	pool := pgtest.Open(t)
	store := yahoolink.NewPGStore(pool)
	ctx := context.Background()
	instance := newResetOfferInstance()
	alice := insertResetOfferUser(t, pool, instance, "alice", true)
	bob := insertResetOfferUser(t, pool, instance, "bob", true)

	var out bytes.Buffer
	require.NoError(t, resetTeamOffer(ctx, &out, store, instance, "alice"))
	assert.Equal(t, "Reset alice's team offer; it opens again at their next visit.\n", out.String())
	assert.False(t, teamOfferSeen(t, pool, alice))
	assert.True(t, teamOfferSeen(t, pool, bob), "another user's offer stays seen")

	out.Reset()
	require.NoError(t, resetTeamOffer(ctx, &out, store, instance, "alice"))
	assert.Equal(t, "alice has not seen the team offer yet; it opens at their next visit.\n", out.String())
}

func TestResetTeamOffer_UnknownUser(t *testing.T) {
	pool := pgtest.Open(t)
	instance := newResetOfferInstance()
	insertResetOfferUser(t, pool, newResetOfferInstance(), "alice", true)

	var out bytes.Buffer
	err := resetTeamOffer(context.Background(), &out, yahoolink.NewPGStore(pool), instance, "alice")
	require.ErrorIs(t, err, yahoolink.ErrUserNotFound, "a user of another instance is not found")
	assert.Empty(t, out.String())
}

func TestResetTeamOffer_EmptyUsernameResetsNobody(t *testing.T) {
	pool := pgtest.Open(t)
	instance := newResetOfferInstance()
	alice := insertResetOfferUser(t, pool, instance, "alice", true)

	var out bytes.Buffer
	err := resetTeamOffer(context.Background(), &out, yahoolink.NewPGStore(pool), instance, "")
	require.ErrorIs(t, err, yahoolink.ErrUserNotFound)
	assert.True(t, teamOfferSeen(t, pool, alice), "an empty username resets no user")
}

func TestResetTeamOffer_AllUsers(t *testing.T) {
	pool := pgtest.Open(t)
	instance := newResetOfferInstance()
	seen := []pgtype.UUID{
		insertResetOfferUser(t, pool, instance, "alice", true),
		insertResetOfferUser(t, pool, instance, "bob", true),
	}
	pending := insertResetOfferUser(t, pool, instance, "carol", false)
	elsewhere := insertResetOfferUser(t, pool, newResetOfferInstance(), "dave", true)

	var out bytes.Buffer
	require.NoError(t, resetInstanceTeamOffers(context.Background(), &out, yahoolink.NewPGStore(pool), instance))
	assert.Equal(t, fmt.Sprintf("Reset the team offer of 2 user(s) on instance %q.\n", instance), out.String())
	for _, u := range append(seen, pending) {
		assert.False(t, teamOfferSeen(t, pool, u))
	}
	assert.True(t, teamOfferSeen(t, pool, elsewhere), "another instance's user keeps the offer seen")
}
