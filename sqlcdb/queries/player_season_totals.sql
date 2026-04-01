-- =============================================================================
-- Player Season Totals Queries (career stats from all leagues)
-- =============================================================================

-- name: UpsertPlayerSeasonTotalBatch :batchexec
INSERT INTO player_season_totals (
    player_id, season, game_type, league_abbrev, team_name, sequence,
    games_played, goals, assists, points, plus_minus, pim
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (player_id, season, game_type, league_abbrev, sequence) DO UPDATE SET
    team_name = EXCLUDED.team_name,
    games_played = EXCLUDED.games_played,
    goals = EXCLUDED.goals,
    assists = EXCLUDED.assists,
    points = EXCLUDED.points,
    plus_minus = EXCLUDED.plus_minus,
    pim = EXCLUDED.pim,
    updated_at = NOW()
WHERE (player_season_totals.team_name, player_season_totals.games_played,
       player_season_totals.goals, player_season_totals.assists,
       player_season_totals.points, player_season_totals.plus_minus,
       player_season_totals.pim)
      IS DISTINCT FROM
      (EXCLUDED.team_name, EXCLUDED.games_played,
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
