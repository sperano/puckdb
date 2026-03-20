-- =============================================================================
-- Fantasy Analysis Queries (backed by views from migration 000009)
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
