-- =============================================================================
-- Fantasy Analysis Queries (backed by views defined in the schema migration)
-- =============================================================================

-- =============================================================================
-- Roster-Player Bridge
-- =============================================================================

-- name: GetYahooRosterWithPlayers :many
SELECT * FROM yahoo_roster_players
WHERE league_id = $1 AND team_id = $2 AND date = $3
ORDER BY selected_position, last_name;

-- name: GetYahooRosteredPlayerIDs :many
SELECT DISTINCT yahoo_player_id FROM yahoo_roster_players
WHERE league_id = $1 AND date = $2;

-- =============================================================================
-- Season Totals & Roto Standings
-- =============================================================================

-- name: GetYahooSeasonTeamTotals :many
SELECT * FROM yahoo_season_team_totals
WHERE league_id = $1
ORDER BY goals + assists DESC;

-- name: GetYahooRotoStandings :many
SELECT * FROM yahoo_roto_standings
WHERE league_id = $1
ORDER BY total_roto_pts DESC;

-- =============================================================================
-- Skater Stats
-- =============================================================================

-- name: GetSkaterSeasonStats :many
SELECT * FROM skater_season_stats
WHERE season = $1
ORDER BY points DESC
LIMIT $2;

-- name: GetSkaterSeasonStatsBySort :many
SELECT * FROM skater_season_stats
WHERE season = sqlc.arg(season)
  AND (sqlc.narg(position)::public.player_position IS NULL
       OR position = sqlc.narg(position)::public.player_position)
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'points' THEN points::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'goals' THEN goals::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'assists' THEN assists::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'plus_minus' THEN plus_minus::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'pim' THEN pim::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'sog' THEN sog::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'ppp' THEN ppp::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'ppg' THEN ppg::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'hits' THEN hits::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'blocks' THEN blocks::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'gp' THEN gp::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'avg_toi_min' THEN avg_toi_min::numeric END DESC NULLS LAST,
  points DESC NULLS LAST, last_name, first_name, player_id
LIMIT sqlc.arg(result_limit);

-- name: GetSkaterRecentStats :many
SELECT * FROM skater_recent_stats
ORDER BY points DESC
LIMIT $1;

-- name: GetSkaterRecentStatsBySeason :many
SELECT * FROM skater_recent_stats
WHERE season = $1
ORDER BY points DESC
LIMIT $2;

-- =============================================================================
-- Goalie Stats
-- =============================================================================

-- name: GetGoalieSeasonStats :many
SELECT * FROM goalie_season_stats
WHERE season = $1
ORDER BY wins DESC
LIMIT $2;

-- name: GetGoalieSeasonStatsBySort :many
SELECT * FROM goalie_season_stats
WHERE season = sqlc.arg(season)
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'wins' THEN wins::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'losses' THEN losses::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'gaa' THEN gaa::numeric END ASC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'sv_pct' THEN sv_pct::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'saves' THEN saves::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'shots_against' THEN shots_against::numeric END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'gp' THEN gp::numeric END DESC NULLS LAST,
  wins DESC NULLS LAST, last_name, first_name, player_id
LIMIT sqlc.arg(result_limit);

-- name: GetGoalieRecentStats :many
SELECT * FROM goalie_recent_stats
ORDER BY wins DESC
LIMIT $1;

-- name: GetGoalieRecentStatsBySeason :many
SELECT * FROM goalie_recent_stats
WHERE season = $1
ORDER BY wins DESC
LIMIT $2;

-- =============================================================================
-- Waiver Wire Analysis
-- =============================================================================

-- name: GetUnrosteredSkaters :many
SELECT s.* FROM skater_recent_stats s
WHERE s.yahoo_id IS NOT NULL
  AND s.season = $1
  AND s.yahoo_id NOT IN (
    SELECT yahoo_player_id FROM yahoo_roster_players
    WHERE league_id = $2 AND date = $3
  )
ORDER BY s.points DESC
LIMIT $4;

-- name: GetUnrosteredGoalies :many
SELECT g.* FROM goalie_recent_stats g
WHERE g.yahoo_id IS NOT NULL
  AND g.season = $1
  AND g.yahoo_id NOT IN (
    SELECT yahoo_player_id FROM yahoo_roster_players
    WHERE league_id = $2 AND date = $3
  )
ORDER BY g.wins DESC
LIMIT $4;

-- =============================================================================
-- Recent Form
-- =============================================================================
-- Per-player totals of one season and game type over the `days` calendar days ending at
-- as_of (both ends inclusive). Unlike skater_recent_stats/goalie_recent_stats,
-- whose window is fixed to the 30 days before CURRENT_DATE, the window is a
-- parameter. last_team_id is the club of the player's last game in the window.
-- positions (any of; NULL for all) filters skaters by players.position; it
-- is text[] because pgx cannot encode an array of an unregistered enum.

-- name: GetSkaterRecentForm :many
SELECT gss.player_id,
       p.first_name,
       p.last_name,
       p.yahoo_id,
       p.position,
       (array_agg(gss.team_id ORDER BY g.game_date DESC, g.id DESC))[1]::bigint AS last_team_id,
       count(DISTINCT gss.game_id) AS gp,
       sum(gss.goals) AS goals,
       sum(gss.assists) AS assists,
       sum(gss.points) AS points,
       sum(gss.plus_minus) AS plus_minus,
       sum(gss.penalty_minutes) AS pim,
       sum(gss.shots_on_goal) AS sog,
       sum(gss.power_play_points) AS ppp,
       sum(gss.power_play_goals) AS ppg,
       sum(gss.hits) AS hits,
       sum(gss.blocked_shots) AS blocks,
       round(sum(gss.toi_seconds)::numeric / count(DISTINCT gss.game_id) / 60, 1) AS avg_toi_min
FROM game_skater_stats gss
JOIN games g ON g.id = gss.game_id
JOIN players p ON p.id = gss.player_id
WHERE g.season = sqlc.arg(season)
  AND g.game_type = sqlc.arg(game_type)::public.game_type
  AND g.game_date > sqlc.arg(as_of)::date - sqlc.arg(days)::integer
  AND g.game_date <= sqlc.arg(as_of)::date
  AND (sqlc.narg(positions)::text[] IS NULL
       OR p.position::text = ANY(sqlc.narg(positions)::text[]))
GROUP BY gss.player_id, p.first_name, p.last_name, p.yahoo_id, p.position
ORDER BY points DESC, goals DESC, p.last_name, p.first_name, gss.player_id
LIMIT sqlc.arg(result_limit);

-- name: GetGoalieRecentForm :many
-- Appearances shorter than min_toi_seconds (relief stints) are left out, as
-- in goalie_recent_stats.
SELECT ggs.player_id,
       p.first_name,
       p.last_name,
       p.yahoo_id,
       (array_agg(ggs.team_id ORDER BY g.game_date DESC, g.id DESC))[1]::bigint AS last_team_id,
       count(DISTINCT ggs.game_id) AS gp,
       count(*) FILTER (WHERE ggs.decision = 'W') AS wins,
       count(*) FILTER (WHERE ggs.decision = 'L') AS losses,
       count(*) FILTER (WHERE ggs.decision = 'OTL') AS ot_losses,
       sum(ggs.goals_against) AS ga,
       sum(ggs.saves) AS saves,
       sum(ggs.shots_against) AS shots_against,
       round(sum(ggs.goals_against)::numeric / NULLIF(sum(ggs.toi_seconds), 0) * 3600, 2) AS gaa,
       round(sum(ggs.saves)::numeric / NULLIF(sum(ggs.shots_against), 0), 3) AS sv_pct
FROM game_goalie_stats ggs
JOIN games g ON g.id = ggs.game_id
JOIN players p ON p.id = ggs.player_id
WHERE g.season = sqlc.arg(season)
  AND g.game_type = sqlc.arg(game_type)::public.game_type
  AND g.game_date > sqlc.arg(as_of)::date - sqlc.arg(days)::integer
  AND g.game_date <= sqlc.arg(as_of)::date
  AND ggs.toi_seconds >= sqlc.arg(min_toi_seconds)::integer
GROUP BY ggs.player_id, p.first_name, p.last_name, p.yahoo_id
ORDER BY wins DESC, sv_pct DESC NULLS LAST, p.last_name, p.first_name, ggs.player_id
LIMIT sqlc.arg(result_limit);
