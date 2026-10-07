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
