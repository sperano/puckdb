-- name: GetAllNHLSeasons :many
SELECT id, standings_start, standings_end
FROM nhl_seasons
ORDER BY id DESC;

-- name: GetNHLSeason :one
SELECT id, standings_start, standings_end
FROM nhl_seasons
WHERE id = $1;

-- name: UpsertNHLSeason :exec
INSERT INTO nhl_seasons (id, standings_start, standings_end)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET
    standings_start = EXCLUDED.standings_start,
    standings_end = EXCLUDED.standings_end;

-- name: CountNHLSeasons :one
SELECT COUNT(*) FROM nhl_seasons;

-- name: GetNHLSeasonTeams :many
SELECT
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
FROM nhl_season_teams
WHERE season_id = $1
ORDER BY division_name, full_name;

-- name: GetNHLSeasonTeamsByDivision :many
SELECT
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
FROM nhl_season_teams
WHERE season_id = $1 AND division_name = $2
ORDER BY full_name;

-- name: GetNHLSeasonTeam :one
SELECT
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
FROM nhl_season_teams
WHERE season_id = $1 AND team_id = $2;

-- name: GetNHLTeamHistory :many
SELECT
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
FROM nhl_season_teams
WHERE franchise_id = $1
ORDER BY season_id DESC;

-- name: UpsertNHLSeasonTeam :exec
INSERT INTO nhl_season_teams (
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (season_id, team_id) DO UPDATE SET
    franchise_id = EXCLUDED.franchise_id,
    full_name = EXCLUDED.full_name,
    abbrev = EXCLUDED.abbrev,
    logo_url = EXCLUDED.logo_url,
    division_name = EXCLUDED.division_name,
    division_abbrev = EXCLUDED.division_abbrev,
    conference_name = EXCLUDED.conference_name,
    conference_abbrev = EXCLUDED.conference_abbrev;

-- name: CountNHLSeasonTeamsForSeason :one
SELECT COUNT(*) FROM nhl_season_teams WHERE season_id = $1;

-- name: CountNHLSeasonTeams :one
SELECT COUNT(*) FROM nhl_season_teams;

-- name: GetDistinctDivisions :many
SELECT DISTINCT division_name, division_abbrev, conference_name, conference_abbrev
FROM nhl_season_teams
WHERE season_id = $1
ORDER BY conference_name, division_name;
