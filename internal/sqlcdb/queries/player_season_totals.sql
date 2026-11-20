-- =============================================================================
-- Player Season Totals Queries (career stats from all leagues)
-- =============================================================================

-- name: UpsertPlayerSeasonTotalBatch :batchexec
INSERT INTO player_season_totals (
    player_id, season, game_type, league_abbrev, team_name, team_id, sequence,
    games_played, goals, assists, points, plus_minus, pim
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (player_id, season, game_type, league_abbrev, sequence) DO UPDATE SET
    team_name = EXCLUDED.team_name,
    team_id = EXCLUDED.team_id,
    games_played = EXCLUDED.games_played,
    goals = EXCLUDED.goals,
    assists = EXCLUDED.assists,
    points = EXCLUDED.points,
    plus_minus = EXCLUDED.plus_minus,
    pim = EXCLUDED.pim,
    updated_at = NOW()
WHERE (player_season_totals.team_name, player_season_totals.team_id,
       player_season_totals.games_played,
       player_season_totals.goals, player_season_totals.assists,
       player_season_totals.points, player_season_totals.plus_minus,
       player_season_totals.pim)
      IS DISTINCT FROM
      (EXCLUDED.team_name, EXCLUDED.team_id,
       EXCLUDED.games_played,
       EXCLUDED.goals, EXCLUDED.assists,
       EXCLUDED.points, EXCLUDED.plus_minus,
       EXCLUDED.pim);

-- name: GetPlayerSeasonTotals :many
SELECT * FROM player_season_totals
WHERE player_id = $1
ORDER BY season DESC, game_type, league_abbrev, sequence;

-- name: GetPlayerSeasonTotalsByLeague :many
SELECT * FROM player_season_totals
WHERE player_id = $1 AND league_abbrev = $2
ORDER BY season DESC, game_type, sequence;

-- name: GetPlayerNHLSeasonTotals :many
SELECT * FROM player_season_totals
WHERE player_id = $1 AND league_abbrev = 'NHL'
ORDER BY season DESC, game_type, sequence;

-- name: CountPlayerSeasonTotals :one
SELECT COUNT(*) FROM player_season_totals;

-- name: ListNamesakeNHLSeasons :many
-- The NHL seasons (start years) each other player with the same normalized
-- name played at least one regular-season or playoff game in, for the Yahoo
-- matcher's seasons step (matching.MatchYahooIDBySeasons).
SELECT DISTINCT t.player_id, (t.season / 10000)::integer AS start_year
FROM players p
JOIN player_season_totals t ON t.player_id = p.id
WHERE p.first_name_normalized = sqlc.arg(first_name_normalized)
  AND p.last_name_normalized = sqlc.arg(last_name_normalized)
  AND p.id <> sqlc.arg(exclude_player_id)
  AND t.league_abbrev = 'NHL'
  AND t.game_type IN ('regular_season', 'playoffs')
  AND t.games_played > 0
ORDER BY t.player_id, start_year;
