package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Fixture identities of the freshness tests.
const (
	freshnessSeason = 20262027
	// freshnessStandingsSeason is a season no other test uses: its seasons
	// and season_teams rows, which no truncated table cascades to, are
	// deleted when the test ends.
	freshnessStandingsSeason = 20982099
	freshnessTeamID          = 1
	freshnessPlayerID        = 8_990_001
	freshnessLeagueID        = 990_001
	freshnessLeague          = "466.l.990001"
)

// freshnessTransactionTime is the newest Yahoo transaction: an epoch column,
// not a date, so the dataset keeps its time of day.
var freshnessTransactionTime = time.Date(2026, time.November, 12, 18, 45, 0, 0, time.UTC)

func execFreshness(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

// seedFreshnessGames inserts games of every state and per-game rows such
// that the completed-game, box score, shift and play-by-play datasets each
// name another game.
func seedFreshnessGames(t *testing.T, pool *pgxpool.Pool) {
	execFreshness(t, pool, `INSERT INTO players (id, first_name, last_name) VALUES ($1, 'Test', 'Freshness')
		ON CONFLICT (id) DO NOTHING`, freshnessPlayerID)
	execFreshness(t, pool, `
		INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id) VALUES
			(2026020099, $1, 'regular_season', '2026-11-06', 'OFF', 1, 2),
			(2026020100, $1, 'regular_season', '2026-11-08', 'OFF', 1, 2),
			(2026020101, $1, 'regular_season', '2026-11-10', 'FINAL', 1, 2),
			(2026020102, $1, 'regular_season', '2026-11-12', 'OFF', 1, 2),
			(2026020103, $1, 'regular_season', '2026-11-13', 'LIVE', 1, 2),
			(2026020104, $1, 'regular_season', '2026-11-20', 'FUT', 1, 2)`, freshnessSeason)
	execFreshness(t, pool, `
		INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, "position")
		VALUES (2026020100, $1, 1, true, 9, 'C'), (2026020101, $1, 1, true, 9, 'C')`, freshnessPlayerID)
	execFreshness(t, pool, `
		INSERT INTO shifts (id, game_id, player_id, team_id, period, start_time, end_time, duration,
			shift_number, type_code, detail_code, event_number)
		VALUES (1, 2026020100, $1, 1, 1, '00:00', '00:45', '00:45', 1, '517', '0', 1),
			(2, 2026020103, $1, 1, 1, '00:00', '00:45', '00:45', 1, '517', '0', 1)`, freshnessPlayerID)
	execFreshness(t, pool, `
		INSERT INTO play_events (game_id, event_id, period, period_type, time_in_period, time_remaining,
			type_desc_key, sort_order)
		VALUES (2026020099, 1, 1, 'REG', '00:00', '20:00', 'faceoff', 1),
			(2026020100, 1, 1, 'REG', '00:00', '20:00', 'faceoff', 1)`)
}

// seedFreshnessNHLDays inserts day imports and standings whose insertion
// times run opposite to their dates, on days no game or other dataset uses.
func seedFreshnessNHLDays(t *testing.T, pool *pgxpool.Pool) {
	execFreshness(t, pool, `
		INSERT INTO nhl_day_imports (season, date, importer_version, inputs_hash, input_files, imported_at)
		VALUES ($1, '2026-11-11', 1, 'h', 1, '2026-11-11T06:00:00Z'),
			($1, '2026-11-09', 1, 'h', 1, '2026-11-12T06:00:00Z')`, freshnessSeason)
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), `DELETE FROM standings_snapshots WHERE season = $1`, freshnessStandingsSeason)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), `DELETE FROM season_teams WHERE season = $1`, freshnessStandingsSeason)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), `DELETE FROM seasons WHERE id = $1`, freshnessStandingsSeason)
		require.NoError(t, err)
	})
	execFreshness(t, pool, `INSERT INTO seasons (id, standings_start, standings_end)
		VALUES ($1, '2098-10-01', '2099-06-30')`, freshnessStandingsSeason)
	execFreshness(t, pool, `INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
		VALUES ($1, $2, 'Freshness Test', 'FRS', 'Test', 'T')`, freshnessStandingsSeason, freshnessTeamID)
	execFreshness(t, pool, `
		INSERT INTO standings_snapshots (season, date, team_id, team_abbrev, wins, losses, ot_losses, points,
			division_abbrev, division_name, created_at)
		VALUES ($1, '2026-11-13', $2, 'FRS', 1, 0, 0, 2, 'T', 'Test', '2026-11-15T10:00:00Z'),
			($1, '2026-11-14', $2, 'FRS', 1, 1, 0, 2, 'T', 'Test', '2026-11-14T10:00:00Z')`,
		freshnessStandingsSeason, freshnessTeamID)
}

// seedFreshnessYahoo inserts rosters, summaries and transactions whose
// insertion times run opposite to their dates, and a newest summary without
// a standings snapshot.
func seedFreshnessYahoo(t *testing.T, pool *pgxpool.Pool) {
	execFreshness(t, pool, `INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'freshness', $3)`,
		freshnessLeagueID, freshnessLeague, freshnessSeason)
	execFreshness(t, pool, `INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, 1, $2, 'Team 1')`,
		freshnessLeagueID, freshnessLeague+".t.1")
	execFreshness(t, pool, `
		INSERT INTO yahoo_team_rosters (league_id, team_id, date, player_id, created_at)
		VALUES ($1, 1, '2026-11-12', 1, '2026-11-12T10:00:00Z'), ($1, 1, '2026-11-11', 1, '2026-11-14T10:00:00Z')`,
		freshnessLeagueID)
	execFreshness(t, pool, `
		INSERT INTO yahoo_team_summaries (league_id, team_id, date, standings_captured_at, created_at)
		VALUES ($1, 1, '2026-11-10', '2026-11-10T12:00:00Z', '2026-11-14T10:00:00Z'),
			($1, 1, '2026-11-13', NULL, '2026-11-13T10:00:00Z')`, freshnessLeagueID)
	execFreshness(t, pool, `
		INSERT INTO yahoo_transactions (league_id, transaction_key, type, "timestamp", created_at)
		VALUES ($1, 't1', 'add', $2, '2026-11-01T00:00:00Z'), ($1, 't2', 'drop', $3, '2026-11-14T00:00:00Z')`,
		freshnessLeagueID, freshnessTransactionTime.Unix(),
		time.Date(2026, time.November, 9, 15, 0, 0, 0, time.UTC).Unix())
}

// seedFreshnessNews inserts an article last seen late in the evening in
// Montreal, and events whose first reports run opposite to their last.
func seedFreshnessNews(t *testing.T, pool *pgxpool.Pool) {
	execFreshness(t, pool, `
		INSERT INTO news_articles (publisher, external_id, source_id, kind, url, first_seen_at, last_seen_at)
		VALUES ('nhl', 'freshness-1', 'nhl', 'official', 'https://example.test/1',
			'2026-11-10T08:00:00Z', '2026-11-13T23:30:00-05:00')`)
	execFreshness(t, pool, `
		INSERT INTO news_events (nhl_player_id, player_name, event_type, report_status, first_reported_at, last_reported_at)
		VALUES ($1, 'Test Freshness', 'injury', 'confirmed', '2026-11-01T10:00:00Z', '2026-11-12T10:00:00Z'),
			($1, 'Test Freshness', 'injury', 'reported', '2026-11-11T08:00:00Z', '2026-11-11T08:00:00Z')`,
		freshnessPlayerID)
}

// seedFreshnessDraftRankings inserts ranking snapshots whose creation times
// run opposite to their as-of times.
func seedFreshnessDraftRankings(t *testing.T, pool *pgxpool.Pool) {
	var projectionID string
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO projection_snapshots (target_season, as_of, model_version, config_hash, source_data_hash,
			lookback_seasons, season_decay, skater_prior_toi_seconds, goalie_prior_shots, goalie_shutout_min_toi,
			max_games, interval_z, minimum_uncertainty, maximum_uncertainty, minimum_history_games)
		VALUES ($1, '2026-11-12T00:00:00Z', 'freshness-test', 'c', 's', 3, 0.5, 1, 1, 1, 82, 1, 0, 1, 1)
		RETURNING id::text`, freshnessSeason).Scan(&projectionID))
	execFreshness(t, pool, `
		INSERT INTO draft_ranking_snapshots (season, league_id, league_key, identity, rules_hash,
			projection_snapshot_id, as_of, created_at, meta)
		VALUES ($1, $2, $3, 'a', 'r', $4, '2026-11-13T09:00:00Z', '2026-11-13T09:05:00Z', '{}'),
			($1, $2, $3, 'b', 'r', $4, '2026-11-12T09:00:00Z', '2026-11-14T00:00:00Z', '{}')`,
		freshnessSeason, freshnessLeagueID, freshnessLeague, projectionID)
}

// seedFreshnessMauriceTurns inserts a turn completed hours after it started.
func seedFreshnessMauriceTurns(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	var userID, conversationID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO app_users (instance, auth_provider, auth_subject, username)
		VALUES ('freshness-test', 'authentik', 'freshness', 'u') RETURNING id::text`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO maurice_conversations (user_id) VALUES ($1) RETURNING id::text`, userID).Scan(&conversationID))
	execFreshness(t, pool, `
		INSERT INTO maurice_turns (conversation_id, user_id, turn_number, idempotency_key, request_hash, status,
			started_at, completed_at)
		VALUES ($1, $2, 0, 'k0', '\x00', 'succeeded', '2026-11-13T20:00:00Z', '2026-11-14T02:00:00Z')`,
		conversationID, userID)
}
