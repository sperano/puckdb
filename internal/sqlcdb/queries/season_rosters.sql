-- =============================================================================
-- Season Rosters Queries
-- =============================================================================

-- name: UpsertSeasonRosterBatch :batchexec
INSERT INTO season_rosters (
    season, team_id, player_id,
    position, shoots_catches, sweater_number,
    height_inches, weight_pounds,
    birth_date, birth_city, birth_state_province, birth_country
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (season, team_id, player_id) DO UPDATE SET
    position = EXCLUDED.position,
    shoots_catches = EXCLUDED.shoots_catches,
    sweater_number = EXCLUDED.sweater_number,
    height_inches = EXCLUDED.height_inches,
    weight_pounds = EXCLUDED.weight_pounds,
    birth_date = EXCLUDED.birth_date,
    birth_city = EXCLUDED.birth_city,
    birth_state_province = EXCLUDED.birth_state_province,
    birth_country = EXCLUDED.birth_country,
    updated_at = NOW()
WHERE (season_rosters.position, season_rosters.shoots_catches,
       season_rosters.sweater_number, season_rosters.height_inches,
       season_rosters.weight_pounds, season_rosters.birth_date,
       season_rosters.birth_city, season_rosters.birth_state_province,
       season_rosters.birth_country)
      IS DISTINCT FROM
      (EXCLUDED.position, EXCLUDED.shoots_catches,
       EXCLUDED.sweater_number, EXCLUDED.height_inches,
       EXCLUDED.weight_pounds, EXCLUDED.birth_date,
       EXCLUDED.birth_city, EXCLUDED.birth_state_province,
       EXCLUDED.birth_country);

-- name: GetSeasonRosterByTeam :many
SELECT r.*, p.first_name, p.last_name
FROM season_rosters r
JOIN players p ON r.player_id = p.id
WHERE r.season = $1 AND r.team_id = $2
ORDER BY r.position, p.last_name;

-- name: GetSeasonRosterByPlayer :many
SELECT r.*, st.full_name as team_name, st.abbrev as team_abbrev
FROM season_rosters r
JOIN season_teams st ON st.season = r.season AND st.team_id = r.team_id
WHERE r.player_id = $1
ORDER BY r.season DESC;

-- name: CountSeasonRosters :one
SELECT COUNT(*) FROM season_rosters;

-- name: CountSeasonRostersBySeason :one
SELECT COUNT(*) FROM season_rosters WHERE season = $1;

-- name: GetSeasonTeamAbbrevs :many
-- NHL clubs only: season_teams also holds international/national teams
-- (team_kind = 'international') referenced by player_season_totals, which
-- have no rosters, club stats, or club schedules to fetch.
SELECT team_id, abbrev FROM season_teams
WHERE season = $1 AND team_kind = 'nhl'
ORDER BY abbrev;

-- name: GetSeasonRosterCoverage :one
-- TEMPORARY: backs the stand-in draft pool's roster-season fallback
-- (internal/draft/standin_pool.go, config.LeagueMetadataSource). Reports how
-- many of a season's NHL clubs have at least one season_rosters row, so a
-- partial import (a cancelled sync, or camp rosters not all published yet)
-- can be told apart from a complete one and the caller can fall back to the
-- prior season instead of silently ranking a pool missing whole teams.
-- Remove together with temporary_metadata_from once Yahoo access returns.
SELECT
    (SELECT COUNT(*) FROM season_teams nt
        WHERE nt.season = sqlc.arg(season)::integer AND nt.team_kind = 'nhl') AS nhl_teams,
    (SELECT COUNT(DISTINCT r.team_id) FROM season_rosters r
        JOIN season_teams st ON st.season = r.season AND st.team_id = r.team_id
        WHERE r.season = sqlc.arg(season)::integer AND st.team_kind = 'nhl') AS teams_with_rosters;

-- name: ListSeasonRosterPoolCandidates :many
-- TEMPORARY: backs the stand-in draft pool built from NHL rosters while
-- Yahoo has none (internal/draft/standin_pool.go, config.LeagueMetadataSource).
-- Remove together with temporary_metadata_from once Yahoo access returns.
--
-- One row per (season, team, player) roster slot; a player rostered by more
-- than one team in the season appears once per team, and the caller
-- deduplicates by picking the most recently updated row. position prefers
-- the roster's own position but falls back to players.position, because
-- season_rosters.position is NULL for rows imported from rosters cached
-- before positions were decoded (and for any player the endpoint sends
-- without a position code) while players.position is populated for
-- almost everyone;
-- the caller excludes a player whose position is still unresolved (NULL or
-- 'F') rather than passing it through unpositioned, which would otherwise
-- fail the whole league in the projection model (position is required).
-- has_skater_history and has_goalie_history mirror the tables, state filter
-- and season window ListProjectionSkaterHistory/ListProjectionGoalieHistory
-- read (min_season is projection.Config.HistoryFloorSeason of the league's
-- target season; target_season excludes the target season itself, which
-- hasn't been read as history yet but would otherwise start passing once
-- its games are played), so a player excluded here would also fail
-- projection coverage if included. A qualifying game row also guarantees
-- the projection's own per-season GROUP BY would see games_played >= 1, so
-- no separate count check is needed here. Goalie rows count only with time
-- on ice: boxscores also list the dressed backup, and from nhl-baseline-v6
-- the model counts appearances, so a goalie who only ever sat on the bench
-- has no history and would fail coverage.
SELECT
    r.player_id,
    r.team_id,
    COALESCE(r.position, p.position) AS position,
    r.updated_at,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    st.abbrev AS team_abbrev,
    EXISTS (
        SELECT 1 FROM game_skater_stats gss
        JOIN games g ON g.id = gss.game_id
        WHERE gss.player_id = r.player_id
          AND g.game_type = 'regular_season'
          AND g.game_state IN ('FINAL', 'OFF')
          AND g.season >= sqlc.arg(min_season)::integer
          AND g.season < sqlc.arg(target_season)::integer
    ) AS has_skater_history,
    EXISTS (
        SELECT 1 FROM game_goalie_stats ggs
        JOIN games g ON g.id = ggs.game_id
        WHERE ggs.player_id = r.player_id
          AND ggs.toi_seconds > 0
          AND g.game_type = 'regular_season'
          AND g.game_state IN ('FINAL', 'OFF')
          AND g.season >= sqlc.arg(min_season)::integer
          AND g.season < sqlc.arg(target_season)::integer
    ) AS has_goalie_history
FROM season_rosters r
JOIN players p ON p.id = r.player_id
JOIN season_teams st ON st.season = r.season AND st.team_id = r.team_id
WHERE r.season = sqlc.arg(season)::integer
ORDER BY r.player_id, r.updated_at DESC, r.team_id;
