package draftboard

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pgBoardSeason    = 2026
	pgBoardGameKey   = 465
	pgBoardLeagueID  = 5621
	pgBoardRulesHash = "board-rules-v1"
	pgBoardOurTeam   = 1
	pgBoardOpponent  = 2
	pgBoardPickClock = 90
	// pgBoardOldPlayer is only on the older imported roster date.
	pgBoardOldPlayer = 50
)

var (
	pgBoardLeagueKey = fmt.Sprintf("%d.l.%d", pgBoardGameKey, pgBoardLeagueID)
	pgBoardIdentity  = draftwatch.Identity{LeagueKey: pgBoardLeagueKey, Season: pgBoardSeason,
		LeagueID: pgBoardLeagueID, GameKey: pgBoardGameKey}
	// pgBoardSlots give two draft rounds: injured reserve is not drafted.
	pgBoardSlots = []draft.RosterSlot{
		{Position: draft.PositionCenter, Count: 1, Starting: true},
		{Position: draft.SlotBench, Count: 1},
		{Position: draft.SlotInjuredReserve, Count: 1},
	}
	pgBoardRoster = []draft.RosterPlayer{
		{YahooPlayerID: 60, EligiblePositions: []string{draft.PositionCenter}},
		{YahooPlayerID: 70, EligiblePositions: []string{draft.PositionCenter, draft.PositionLeftWing}},
	}
	pgBoardRulesSeenAt = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

// newPGBoardDataSource returns a data source over a private, migrated
// database holding one imported league with its rules, two teams and our
// team's roster on two dates.
func newPGBoardDataSource(t *testing.T) (*PGDataSource, *pgxpool.Pool) {
	t.Helper()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	seedPGBoardLeague(t, pool)
	return NewPGDataSource(pool, nil), pool
}

func seedPGBoardLeague(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	seed := simfixtures.NewSeeder(t, pool)
	rules, err := json.Marshal(draft.Rules{RosterSlots: pgBoardSlots})
	require.NoError(t, err)
	seed.Exec(`INSERT INTO yahoo_league_rule_snapshots
		(season, league_id, league_key, game_key, source, source_season, source_league_key,
		 fetched_at, rules_hash, rules, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $1, $3, $6, $7, $8, $6, $6)`,
		pgBoardSeason, pgBoardLeagueID, pgBoardLeagueKey, pgBoardGameKey, string(draft.SourceYahooAPI),
		pgBoardRulesSeenAt, pgBoardRulesHash, rules)
	seed.Exec(`INSERT INTO yahoo_leagues (id, league_key, name, season, num_teams, draft_type, draft_pick_time)
		VALUES ($1, $2, 'Board league', $3, 2, 'live', $4)`,
		pgBoardLeagueID, pgBoardLeagueKey, pgBoardSeason, pgBoardPickClock)
	for _, team := range []struct {
		id, position int
		owned        bool
	}{{pgBoardOurTeam, 2, true}, {pgBoardOpponent, 1, false}} {
		seed.Exec(`INSERT INTO yahoo_teams (league_id, id, team_key, name, draft_position, is_owned_by_current_login)
			VALUES ($1, $2, $3, $4, $5, $6)`, pgBoardLeagueID, team.id,
			fmt.Sprintf("%s.t.%d", pgBoardLeagueKey, team.id), fmt.Sprintf("Team %d", team.id), team.position, team.owned)
	}
	seed.Exec(`INSERT INTO yahoo_team_rosters (league_id, team_id, date, player_id, eligible_positions)
		VALUES ($1, $2, '2026-10-01', $3, '{C}')`, pgBoardLeagueID, pgBoardOurTeam, pgBoardOldPlayer)
	for _, player := range pgBoardRoster {
		seed.Exec(`INSERT INTO yahoo_team_rosters (league_id, team_id, date, player_id, eligible_positions)
			VALUES ($1, $2, '2026-10-02', $3, $4)`,
			pgBoardLeagueID, pgBoardOurTeam, player.YahooPlayerID, player.EligiblePositions)
	}
}

func TestPGDataSourceLeagueDataReadsImportedLeague(t *testing.T) {
	data, pool := newPGBoardDataSource(t)
	ctx := t.Context()

	league, err := data.LeagueData(ctx, pgBoardIdentity)
	require.NoError(t, err)
	assert.Equal(t, pgBoardRulesHash, league.Rules.Hash)
	assert.Equal(t, pgBoardSlots, league.Rules.Rules.RosterSlots)
	assert.Equal(t, pgBoardOurTeam, league.TeamID)
	assert.Equal(t, "Team 1", league.TeamName)
	assert.Equal(t, 2, league.DraftPosition)
	assert.Equal(t, "live", league.DraftFormat)
	require.NotNil(t, league.PickClock)
	assert.Equal(t, pgBoardPickClock, *league.PickClock)
	assert.Equal(t, pgBoardRoster, league.Roster, "only the latest roster date counts")
	assert.Equal(t, []draftrecommend.PickSlot{
		{Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: pgBoardOpponent},
		{Key: draftsession.PickKey{Round: 1, Pick: 2}, TeamID: pgBoardOurTeam},
		{Key: draftsession.PickKey{Round: 2, Pick: 3}, TeamID: pgBoardOurTeam},
		{Key: draftsession.PickKey{Round: 2, Pick: 4}, TeamID: pgBoardOpponent},
	}, league.EstimatedOrder)

	_, err = pool.Exec(ctx, `UPDATE yahoo_leagues SET is_auction_draft = TRUE, draft_pick_time = NULL WHERE id = $1`,
		pgBoardLeagueID)
	require.NoError(t, err)
	league, err = data.LeagueData(ctx, pgBoardIdentity)
	require.NoError(t, err)
	assert.Equal(t, "auction", league.DraftFormat)
	assert.Nil(t, league.PickClock)
	assert.Nil(t, league.EstimatedOrder, "an auction has no snake order")
}

func TestPGDataSourceLeagueDataMapsMissingAndMismatchedRows(t *testing.T) {
	data, pool := newPGBoardDataSource(t)
	ctx := t.Context()

	_, err := data.LeagueData(ctx, draftwatch.Identity{LeagueKey: "465.l.9", Season: pgBoardSeason, LeagueID: 9})
	require.ErrorIs(t, err, draft.ErrNoRules)

	otherSeason := pgBoardIdentity
	otherSeason.Season++
	_, err = pool.Exec(ctx, `UPDATE yahoo_league_rule_snapshots SET season = $1`, otherSeason.Season)
	require.NoError(t, err)
	_, err = data.LeagueData(ctx, otherSeason)
	require.Error(t, err)
	assert.ErrorContains(t, err, "does not match requested", "the league row belongs to another season")

	_, err = pool.Exec(ctx, `UPDATE yahoo_league_rule_snapshots SET season = $1`, pgBoardSeason)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE yahoo_teams SET is_owned_by_current_login = FALSE`)
	require.NoError(t, err)
	_, err = data.LeagueData(ctx, pgBoardIdentity)
	require.ErrorIs(t, err, ErrNoOwnedTeam)
	assert.NotErrorIs(t, err, pgx.ErrNoRows, "callers must not depend on pgx semantics")
}

func TestPGDataSourceShortlistWritesAndOrdersPlayers(t *testing.T) {
	data, pool := newPGBoardDataSource(t)
	ctx := t.Context()
	_, err := data.Session(ctx, pgBoardIdentity)
	require.NoError(t, err)
	firstAt := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	laterAt := firstAt.Add(time.Minute)

	require.NoError(t, data.SetShortlist(ctx, pgBoardLeagueKey, "465.p.2", 0, true, firstAt))
	require.NoError(t, data.SetShortlist(ctx, pgBoardLeagueKey, "465.p.3", 0, true, laterAt))
	require.NoError(t, data.SetShortlist(ctx, pgBoardLeagueKey, "465.p.1", 0, true, laterAt))
	assertShortlist(t, data, []string{"465.p.2", "465.p.1", "465.p.3"})
	assertShortlistSession(t, pool, 3, laterAt)
	assertShortlistAddedAt(t, pool, "465.p.2", firstAt)

	require.NoError(t, data.SetShortlist(ctx, pgBoardLeagueKey, "465.p.2", 0, true, laterAt.Add(time.Hour)))
	assertShortlistAddedAt(t, pool, "465.p.2", firstAt)
	assertShortlistSession(t, pool, 3, laterAt)

	removedAt := laterAt.Add(time.Minute)
	require.NoError(t, data.SetShortlist(ctx, pgBoardLeagueKey, "465.p.1", 0, false, removedAt))
	require.NoError(t, data.SetShortlist(ctx, pgBoardLeagueKey, "465.p.9", 0, false, removedAt.Add(time.Hour)))
	assertShortlist(t, data, []string{"465.p.2", "465.p.3"})
	assertShortlistSession(t, pool, 4, removedAt)
}

func TestPGDataSourceShortlistMapsVersionAndSessionErrors(t *testing.T) {
	data, pool := newPGBoardDataSource(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	key := draftsession.PickKey{Round: 1, Pick: 1}
	_, _, err := data.ApplyManual(ctx, pgBoardIdentity, draftsession.ManualOperation{Kind: draftsession.ManualAdd,
		Key: key, Pick: &draftsession.Pick{Key: key, TeamID: pgBoardOpponent, PlayerID: 101}}, 0, at)
	require.NoError(t, err)

	err = data.SetShortlist(ctx, pgBoardLeagueKey, "465.p.2", 0, true, at)
	assertVersionConflict(t, err, 0, 1)
	assertShortlist(t, data, []string{})
	assertShortlistSession(t, pool, 1, at)

	err = data.SetShortlist(ctx, "465.l.9", "465.p.2", 0, true, at)
	require.ErrorIs(t, err, draftwatch.ErrSessionNotFound)
	assert.NotErrorIs(t, err, pgx.ErrNoRows, "callers must not depend on pgx semantics")
	assert.NotErrorIs(t, err, ErrVersionConflict)
}

func TestPGServiceStampsShortlistAndWatchWithItsClock(t *testing.T) {
	_, pool := newPGBoardDataSource(t)
	now := time.Date(2026, 10, 4, 16, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	runner := &blockingWatchRunner{started: make(chan struct{}, 1)}
	service := NewPGService(pool, nil, nil, func() WatchRunner { return runner },
		draftwatch.WatchOptions{FinalTimeout: watchTestTimeout}, Options{Now: func() time.Time { return now }})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	ctx := t.Context()

	require.NoError(t, service.SetShortlist(ctx, pgBoardLeagueKey, pgBoardSeason, "465.p.2", 0, true))
	assertShortlistAddedAt(t, pool, "465.p.2", now)
	assertShortlistSession(t, pool, 1, now)

	status, err := service.StartWatch(ctx, pgBoardLeagueKey, pgBoardSeason)
	require.NoError(t, err)
	assert.Equal(t, now.UTC(), status.StartedAt)
	<-runner.started
	status, err = service.StopWatch(ctx, pgBoardLeagueKey, pgBoardSeason)
	require.NoError(t, err)
	require.NotNil(t, status.StoppedAt)
	assert.Equal(t, now.UTC(), *status.StoppedAt)

	key := draftsession.PickKey{Round: 1, Pick: 1}
	_, err = service.ApplyManual(ctx, pgBoardLeagueKey, pgBoardSeason, draftsession.ManualOperation{
		Kind: draftsession.ManualAdd, Key: key, Pick: &draftsession.Pick{Key: key, TeamID: pgBoardOpponent, PlayerID: 101},
	}, 3, now)
	assertVersionConflict(t, err, 3, 0)
}

func assertShortlist(t *testing.T, data *PGDataSource, want []string) {
	t.Helper()
	got, err := data.ListShortlist(t.Context(), pgBoardLeagueKey)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// assertShortlistSession checks the session's sync version and update time.
func assertShortlistSession(t *testing.T, pool *pgxpool.Pool, syncVersion int64, updatedAt time.Time) {
	t.Helper()
	var gotSync int64
	var gotUpdated time.Time
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT sync_version, updated_at FROM draft_sessions WHERE league_key = $1`,
		pgBoardLeagueKey).Scan(&gotSync, &gotUpdated))
	assert.Equal(t, syncVersion, gotSync)
	assert.True(t, updatedAt.Equal(gotUpdated), "updated_at = %s, want %s", gotUpdated, updatedAt)
}

func assertShortlistAddedAt(t *testing.T, pool *pgxpool.Pool, playerKey string, want time.Time) {
	t.Helper()
	var got time.Time
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT created_at FROM draft_shortlist WHERE league_key = $1 AND player_key = $2`,
		pgBoardLeagueKey, playerKey).Scan(&got))
	assert.True(t, want.Equal(got), "created_at = %s, want %s", got, want)
}
