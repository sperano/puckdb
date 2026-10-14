-- name: DeleteEvenStrengthPairTOIForGame :exec
DELETE FROM even_strength_pair_toi
WHERE game_id = sqlc.arg(game_id);

-- name: DeleteEvenStrengthSkaterGamesForGame :exec
DELETE FROM even_strength_skater_games
WHERE game_id = sqlc.arg(game_id);

-- name: ListEvenStrengthShiftsForGame :many
-- One game's regular shifts (type 517) of players with a box-score row on
-- the shift's club, the input of internal/evenstrength's segment derivation.
-- is_skater tells a skater (game_skater_stats row) from a goalie
-- (game_goalie_stats row only); shifts of anyone else are dropped. Clock
-- strings are returned as stored and parsed in Go.
SELECT
    s.player_id,
    s.team_id,
    s.period,
    s.start_time,
    s.end_time,
    (stats.player_id IS NOT NULL)::boolean AS is_skater
FROM shifts s
LEFT JOIN game_skater_stats stats
  ON stats.game_id = s.game_id
 AND stats.player_id = s.player_id
 AND stats.team_id = s.team_id
LEFT JOIN game_goalie_stats goalies
  ON goalies.game_id = s.game_id
 AND goalies.player_id = s.player_id
 AND goalies.team_id = s.team_id
WHERE s.game_id = sqlc.arg(game_id)
  AND s.type_code = '517'
  AND (stats.player_id IS NOT NULL OR goalies.player_id IS NOT NULL);

-- name: ListEvenStrengthGoalsForGame :many
-- One game's goals with their clock, situation code and credited players,
-- for internal/evenstrength's even-strength point credit.
SELECT
    period,
    time_in_period,
    situation_code,
    scoring_player_id,
    assist1_player_id,
    assist2_player_id
FROM play_events
WHERE game_id = sqlc.arg(game_id)
  AND type_desc_key = 'goal';

-- name: InsertEvenStrengthPairTOI :execrows
-- Store one game's shared even-strength TOI rows computed by
-- internal/evenstrength, one array entry per (player, teammate) row. Callers
-- delete the game's rows first, in the same transaction (see
-- DeleteEvenStrengthPairTOIForGame).
INSERT INTO even_strength_pair_toi (
    game_id, player_id, teammate_id, shared_toi_seconds
)
-- The arrays have one entry per row; set-returning functions in the select
-- list advance in lockstep.
SELECT sqlc.arg(game_id)::bigint,
       unnest(sqlc.arg(player_ids)::bigint[]),
       unnest(sqlc.arg(teammate_ids)::bigint[]),
       unnest(sqlc.arg(shared_toi_seconds)::integer[]);

-- name: InsertEvenStrengthSkaterGames :execrows
-- Store one game's per-skater even-strength TOI and points computed by
-- internal/evenstrength, one array entry per skater row. Callers delete the
-- game's rows first, in the same transaction (see
-- DeleteEvenStrengthSkaterGamesForGame).
INSERT INTO even_strength_skater_games (
    game_id, player_id, team_id, toi_seconds, points
)
-- The arrays have one entry per row, as in InsertEvenStrengthPairTOI.
SELECT sqlc.arg(game_id)::bigint,
       unnest(sqlc.arg(player_ids)::bigint[]),
       unnest(sqlc.arg(team_ids)::bigint[]),
       unnest(sqlc.arg(toi_seconds)::integer[]),
       unnest(sqlc.arg(points)::integer[]);

-- name: ListPlayerEvenStrengthLinemates :many
-- The skaters one player shared even-strength ice with, most shared time
-- first, for the get_player_linemates MCP tool. Games are filtered by
-- season, date range and game type (NULL game type: regular season and
-- playoffs), then the per-game totals stored by internal/evenstrength
-- (even_strength_pair_toi, even_strength_skater_games) are summed per (club,
-- teammate), so a traded player's linemates stay with the club they shared.
-- share_pct is the shared time as a percentage of the player's own
-- even-strength time with that club over the same games.
WITH player_games AS (
    SELECT skater.game_id, skater.team_id, skater.toi_seconds, game.season
    FROM games game
    JOIN even_strength_skater_games skater
      ON skater.game_id = game.id
     AND skater.player_id = sqlc.arg(player_id)
    WHERE (sqlc.narg('season')::int IS NULL OR game.season = sqlc.narg('season'))
      AND (sqlc.narg('start_date')::date IS NULL OR game.game_date >= sqlc.narg('start_date'))
      AND (sqlc.narg('end_date')::date IS NULL OR game.game_date <= sqlc.narg('end_date'))
      AND CASE
          WHEN sqlc.narg('game_type')::game_type IS NULL
              THEN game.game_type IN ('regular_season', 'playoffs')
          ELSE game.game_type = sqlc.narg('game_type')
      END
),
club_totals AS (
    SELECT team_id, sum(toi_seconds)::bigint AS toi_seconds, max(season) AS last_season
    FROM player_games
    GROUP BY team_id
),
shared AS (
    SELECT
        player_game.team_id,
        pair.teammate_id,
        count(*)::integer AS games_together,
        sum(pair.shared_toi_seconds)::bigint AS shared_toi_seconds
    FROM player_games player_game
    JOIN even_strength_pair_toi pair
      ON pair.game_id = player_game.game_id
     AND pair.player_id = sqlc.arg(player_id)
    GROUP BY player_game.team_id, pair.teammate_id
)
SELECT
    shared.team_id,
    club.abbrev AS team_abbrev,
    shared.teammate_id,
    teammate.first_name,
    teammate.last_name,
    teammate.position,
    shared.games_together,
    shared.shared_toi_seconds,
    round(100.0 * shared.shared_toi_seconds / totals.toi_seconds, 1)::float8 AS share_pct
FROM shared
JOIN club_totals totals ON totals.team_id = shared.team_id
LEFT JOIN season_teams club
  ON club.team_id = shared.team_id
 AND club.season = totals.last_season
LEFT JOIN players teammate ON teammate.id = shared.teammate_id
ORDER BY shared.shared_toi_seconds DESC, shared.team_id, shared.teammate_id
LIMIT sqlc.arg(result_limit)::integer;
