package draftwatch

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seasons of the identity fixture: one numeric league ID reused by two
// Yahoo games, as Yahoo does across seasons.
const (
	identityOldSeason  = 2025
	identityOldGameKey = 453
	identityNewSeason  = 2026
	identityNewGameKey = 465
	identityLeagueGap  = time.Hour
)

func TestResolveIdentityRejectsMalformedReferencesBeforeQuerying(t *testing.T) {
	for name, input := range map[string]struct {
		ref    string
		season int
	}{
		"empty":                {"  ", NewestSeason},
		"word":                 {"abc", NewestSeason},
		"zero ID":              {"0", NewestSeason},
		"negative ID":          {"-3", NewestSeason},
		"ID past integer":      {strconv.FormatInt(math.MaxInt32+1, 10), NewestSeason},
		"key without marker":   {"465.x.1001", NewestSeason},
		"key with extra part":  {"465.l.1001.2", NewestSeason},
		"key with word game":   {"abc.l.1001", NewestSeason},
		"key with zero league": {"465.l.0", NewestSeason},
		"negative season":      {"1001", -1},
		"season past integer":  {"1001", math.MaxInt32 + 1},
	} {
		t.Run(name, func(t *testing.T) {
			// A nil pool proves the reference is refused before any query.
			_, err := ResolveIdentity(context.Background(), nil, input.ref, input.season)
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrLeagueNotImported)
		})
	}
}

// TestResolveIdentityOverPostgreSQL covers every reference form against
// imported rule snapshots: a full key names its season, a numeric ID takes
// the requested season or, with NewestSeason, the most recently seen one.
func TestResolveIdentityOverPostgreSQL(t *testing.T) {
	pool := openDraftWatchTestDB(t)
	ctx := context.Background()
	leagueID := seedIdentityLeague(t, pool)
	oldKey := fmt.Sprintf("%d.l.%d", identityOldGameKey, leagueID)
	newKey := fmt.Sprintf("%d.l.%d", identityNewGameKey, leagueID)
	oldIdentity := Identity{LeagueKey: oldKey, Season: identityOldSeason, LeagueID: leagueID, GameKey: identityOldGameKey}
	newIdentity := Identity{LeagueKey: newKey, Season: identityNewSeason, LeagueID: leagueID, GameKey: identityNewGameKey}
	numeric := strconv.Itoa(leagueID)

	for name, tc := range map[string]struct {
		ref    string
		season int
		want   Identity
	}{
		"full key":                       {oldKey, NewestSeason, oldIdentity},
		"full key with its season":       {newKey, identityNewSeason, newIdentity},
		"numeric ID with a season":       {numeric, identityOldSeason, oldIdentity},
		"numeric ID takes newest season": {numeric, NewestSeason, newIdentity},
		"surrounding spaces":             {" " + newKey + " ", NewestSeason, newIdentity},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveIdentity(ctx, pool, tc.ref, tc.season)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	// Newest means seen last, not the latest season: re-importing the old
	// season's rules makes it the one a bare numeric ID resolves to.
	_, err := pool.Exec(ctx, `UPDATE yahoo_league_rule_snapshots SET last_seen_at = last_seen_at + $2 * interval '1 second'
		WHERE league_key = $1`, oldKey, int((2 * identityLeagueGap).Seconds()))
	require.NoError(t, err)
	got, err := ResolveIdentity(ctx, pool, numeric, NewestSeason)
	require.NoError(t, err)
	assert.Equal(t, oldIdentity, got)

	_, err = ResolveIdentity(ctx, pool, oldKey, identityNewSeason)
	require.ErrorIs(t, err, ErrLeagueNotImported)
	assert.ErrorContains(t, err, fmt.Sprintf("the key belongs to season %d", identityOldSeason))
	assert.NotContains(t, err.Error(), "sync its league metadata first")

	for name, tc := range map[string]struct {
		ref    string
		season int
	}{
		"missing full key in a season":   {fmt.Sprintf("%d.l.%d", identityNewGameKey, leagueID+1), identityNewSeason},
		"missing full key":               {fmt.Sprintf("%d.l.%d", identityNewGameKey, leagueID+1), NewestSeason},
		"numeric ID in a missing season": {numeric, identityNewSeason + 1},
		"missing numeric ID":             {strconv.Itoa(leagueID + 1), NewestSeason},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveIdentity(ctx, pool, tc.ref, tc.season)
			require.ErrorIs(t, err, ErrLeagueNotImported)
			assert.ErrorContains(t, err, "sync its league metadata first")
		})
	}
}

// seedIdentityLeague imports rules for one fresh numeric league ID in the
// old and the new season, the new season seen last, and returns the ID.
func seedIdentityLeague(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	ctx := context.Background()
	leagueID := int(time.Now().UnixNano()%(math.MaxInt32-1)) + 1
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM yahoo_league_rule_snapshots WHERE league_id = $1 OR league_id = $1 + 1`, leagueID)
	})
	seenAt := time.Date(identityNewSeason, time.September, 1, 0, 0, 0, 0, time.UTC)
	for _, season := range []struct {
		year, gameKey int
		seen          time.Time
	}{
		{identityOldSeason, identityOldGameKey, seenAt.Add(-identityLeagueGap)},
		{identityNewSeason, identityNewGameKey, seenAt},
	} {
		key := fmt.Sprintf("%d.l.%d", season.gameKey, leagueID)
		_, err := pool.Exec(ctx, `INSERT INTO yahoo_league_rule_snapshots
			(season, league_id, league_key, game_key, source, source_season, source_league_key,
			 fetched_at, rules_hash, rules, first_seen_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $1, $3, $6, 'identity-test', '{}', $6, $6)`,
			season.year, leagueID, key, season.gameKey, string(draft.SourceYahooAPI), season.seen)
		require.NoError(t, err)
	}
	return leagueID
}
