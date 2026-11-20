-- =============================================================================
-- Fantasy team player values (MCP get_yahoo_team_player_values)
-- =============================================================================

-- name: ListYahooLeaguePlayerWindowTotals :many
-- One row per player and league team that rostered them between from_date
-- and to_date: the days on that team, the days in an active slot (any slot
-- not in inactive_slots), the last of those days, and the matched NHL
-- player's regular-season totals in the final games played on those days
-- (Yahoo roster dates are NHL game dates). With active_only, the totals count
-- only the active days. A player with no NHL match has zero totals; name is
-- the NHL player's, else Yahoo's from the league's imported pool, else empty.
-- Faceoffs are counted from the play-by-play of the window's games only.
WITH roster_days AS (
    SELECT r.team_id,
           r.player_id,
           r.player_key,
           r.date,
           r.position_type,
           r.selected_position <> ALL(sqlc.arg(inactive_slots)::text[]) AS active
    FROM yahoo_team_rosters r
    WHERE r.league_id = sqlc.arg(league_id)
      AND r.date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
),
window_games AS (
    SELECT g.id, g.game_date
    FROM games g
    WHERE g.game_type = 'regular_season'
      AND g.game_state IN ('FINAL', 'OFF')
      AND g.game_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
),
faceoffs AS (
    -- One row per (game, player), so the join below cannot multiply rows.
    SELECT f.game_id, f.player_id, SUM(f.won)::bigint AS won, SUM(f.lost)::bigint AS lost
    FROM (
        SELECT e.game_id, e.winning_player_id AS player_id, 1 AS won, 0 AS lost
        FROM window_games w
        JOIN play_events e ON e.game_id = w.id
        WHERE e.type_desc_key = 'faceoff'
        UNION ALL
        SELECT e.game_id, e.losing_player_id AS player_id, 0 AS won, 1 AS lost
        FROM window_games w
        JOIN play_events e ON e.game_id = w.id
        WHERE e.type_desc_key = 'faceoff'
    ) f
    GROUP BY f.game_id, f.player_id
),
skater_days AS (
    SELECT w.game_date,
           s.player_id,
           COUNT(*)::bigint AS games,
           SUM(s.goals)::bigint AS goals,
           SUM(s.assists)::bigint AS assists,
           SUM(s.points)::bigint AS points,
           SUM(s.plus_minus)::bigint AS plus_minus,
           SUM(s.penalty_minutes)::bigint AS penalty_minutes,
           SUM(s.power_play_points)::bigint AS power_play_points,
           SUM(s.shots_on_goal)::bigint AS shots_on_goal,
           SUM(s.hits)::bigint AS hits,
           SUM(s.blocked_shots)::bigint AS blocked_shots,
           COALESCE(SUM(fo.won), 0)::bigint AS faceoffs_won,
           COALESCE(SUM(fo.lost), 0)::bigint AS faceoffs_lost
    FROM window_games w
    JOIN game_skater_stats s ON s.game_id = w.id
    LEFT JOIN faceoffs fo ON fo.game_id = s.game_id AND fo.player_id = s.player_id
    GROUP BY w.game_date, s.player_id
),
goalie_days AS (
    SELECT w.game_date,
           s.player_id,
           -- A game dressed as the backup with no time on ice is not played.
           COUNT(*) FILTER (WHERE s.toi_seconds > 0)::bigint AS games,
           COUNT(*) FILTER (WHERE s.decision = 'W')::bigint AS wins,
           COUNT(*) FILTER (
               WHERE s.starter AND s.goals_against = 0
                 AND s.toi_seconds >= sqlc.arg(minimum_shutout_toi_seconds)::integer
           )::bigint AS shutouts,
           SUM(s.goals_against)::bigint AS goals_against,
           SUM(s.shots_against)::bigint AS shots_against,
           SUM(s.saves)::bigint AS saves,
           SUM(s.toi_seconds)::bigint AS toi_seconds
    FROM window_games w
    JOIN game_goalie_stats s ON s.game_id = w.id
    GROUP BY w.game_date, s.player_id
),
counted_days AS (
    SELECT rd.*, (NOT sqlc.arg(active_only)::boolean OR rd.active) AS counted
    FROM roster_days rd
)
SELECT d.team_id,
       d.player_id AS yahoo_player_id,
       (ARRAY_AGG(d.player_key ORDER BY d.date DESC))[1]::text AS player_key,
       COALESCE((ARRAY_AGG(d.position_type ORDER BY d.date DESC))[1], '')::text AS position_type,
       p.id AS nhl_player_id,
       COALESCE(p."position"::text, '')::text AS nhl_position,
       COALESCE(p.first_name || ' ' || p.last_name, lp.full_name, '')::text AS name,
       COUNT(*)::int AS days,
       COUNT(*) FILTER (WHERE d.active)::int AS active_days,
       MAX(d.date)::date AS last_date,
       COALESCE(SUM(sd.games) FILTER (WHERE d.counted), 0)::bigint AS skater_games,
       COALESCE(SUM(sd.goals) FILTER (WHERE d.counted), 0)::bigint AS goals,
       COALESCE(SUM(sd.assists) FILTER (WHERE d.counted), 0)::bigint AS assists,
       COALESCE(SUM(sd.points) FILTER (WHERE d.counted), 0)::bigint AS points,
       COALESCE(SUM(sd.plus_minus) FILTER (WHERE d.counted), 0)::bigint AS plus_minus,
       COALESCE(SUM(sd.penalty_minutes) FILTER (WHERE d.counted), 0)::bigint AS penalty_minutes,
       COALESCE(SUM(sd.power_play_points) FILTER (WHERE d.counted), 0)::bigint AS power_play_points,
       COALESCE(SUM(sd.shots_on_goal) FILTER (WHERE d.counted), 0)::bigint AS shots_on_goal,
       COALESCE(SUM(sd.hits) FILTER (WHERE d.counted), 0)::bigint AS hits,
       COALESCE(SUM(sd.blocked_shots) FILTER (WHERE d.counted), 0)::bigint AS blocked_shots,
       COALESCE(SUM(sd.faceoffs_won) FILTER (WHERE d.counted), 0)::bigint AS faceoffs_won,
       COALESCE(SUM(sd.faceoffs_lost) FILTER (WHERE d.counted), 0)::bigint AS faceoffs_lost,
       COALESCE(SUM(gd.games) FILTER (WHERE d.counted), 0)::bigint AS goalie_games,
       COALESCE(SUM(gd.wins) FILTER (WHERE d.counted), 0)::bigint AS wins,
       COALESCE(SUM(gd.shutouts) FILTER (WHERE d.counted), 0)::bigint AS shutouts,
       COALESCE(SUM(gd.goals_against) FILTER (WHERE d.counted), 0)::bigint AS goals_against,
       COALESCE(SUM(gd.shots_against) FILTER (WHERE d.counted), 0)::bigint AS shots_against,
       COALESCE(SUM(gd.saves) FILTER (WHERE d.counted), 0)::bigint AS saves,
       COALESCE(SUM(gd.toi_seconds) FILTER (WHERE d.counted), 0)::bigint AS goalie_toi_seconds
FROM counted_days d
LEFT JOIN players p ON p.yahoo_id = d.player_id
LEFT JOIN yahoo_league_players lp ON lp.league_key = sqlc.arg(league_key) AND lp.player_id = d.player_id
LEFT JOIN skater_days sd ON sd.player_id = p.id AND sd.game_date = d.date
LEFT JOIN goalie_days gd ON gd.player_id = p.id AND gd.game_date = d.date
GROUP BY d.team_id, d.player_id, p.id, p."position", p.first_name, p.last_name, lp.full_name
ORDER BY d.team_id, d.player_id;

-- name: ListProjectionValuesByNHLPlayers :many
-- The projected season means of a projection snapshot for the given NHL
-- players, one row per player key and stat; a player projected under two
-- keys has rows for both, ordered by key.
SELECT pp.player_id::bigint AS nhl_player_id, pp.player_key, v.stat, v.mean
FROM projection_players pp
JOIN projection_values v ON v.snapshot_id = pp.snapshot_id AND v.player_key = pp.player_key
WHERE pp.snapshot_id = sqlc.arg(snapshot_id)
  AND pp.player_id = ANY(sqlc.arg(nhl_player_ids)::bigint[])
ORDER BY pp.player_id, pp.player_key, v.stat;
