-- name: GetAllSeasons :many
SELECT id, standings_start, standings_end
FROM seasons
ORDER BY id DESC;

-- name: GetSeason :one
SELECT id, standings_start, standings_end
FROM seasons
WHERE id = $1;

-- name: UpsertSeason :exec
INSERT INTO seasons (id, standings_start, standings_end)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET
    standings_start = EXCLUDED.standings_start,
    standings_end = EXCLUDED.standings_end
WHERE (seasons.standings_start, seasons.standings_end)
      IS DISTINCT FROM
      (EXCLUDED.standings_start, EXCLUDED.standings_end);

-- name: CountSeasons :one
SELECT COUNT(*) FROM seasons;

-- name: GetSeasonTeams :many
SELECT
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
FROM season_teams
WHERE season_id = $1
ORDER BY division_name, full_name;

-- name: GetSeasonTeamsByDivision :many
SELECT
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
FROM season_teams
WHERE season_id = $1 AND division_name = $2
ORDER BY full_name;

-- name: GetSeasonTeam :one
SELECT
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
FROM season_teams
WHERE season_id = $1 AND team_id = $2;

-- name: GetTeamHistory :many
SELECT
    season_id, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
FROM season_teams
WHERE franchise_id = $1
ORDER BY season_id DESC;

-- name: UpsertSeasonTeam :exec
INSERT INTO season_teams (
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
    conference_abbrev = EXCLUDED.conference_abbrev
WHERE (season_teams.franchise_id, season_teams.full_name,
       season_teams.abbrev, season_teams.logo_url,
       season_teams.division_name, season_teams.division_abbrev,
       season_teams.conference_name, season_teams.conference_abbrev)
      IS DISTINCT FROM
      (EXCLUDED.franchise_id, EXCLUDED.full_name,
       EXCLUDED.abbrev, EXCLUDED.logo_url,
       EXCLUDED.division_name, EXCLUDED.division_abbrev,
       EXCLUDED.conference_name, EXCLUDED.conference_abbrev);

-- name: CountSeasonTeamsForSeason :one
SELECT COUNT(*) FROM season_teams WHERE season_id = $1;

-- name: CountSeasonTeams :one
SELECT COUNT(*) FROM season_teams;

-- name: GetDistinctDivisions :many
SELECT DISTINCT division_name, division_abbrev, conference_name, conference_abbrev
FROM season_teams
WHERE season_id = $1
ORDER BY conference_name, division_name;
