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
SELECT * FROM season_teams
WHERE season = $1 AND team_kind = 'nhl'
ORDER BY division_name, full_name;

-- name: GetSeasonTeamsByDivision :many
SELECT * FROM season_teams
WHERE season = $1 AND division_name = $2 AND team_kind = 'nhl'
ORDER BY full_name;

-- name: GetSeasonTeam :one
SELECT * FROM season_teams
WHERE season = $1 AND team_id = $2 AND team_kind = 'nhl';

-- name: GetTeamHistory :many
SELECT * FROM season_teams
WHERE franchise_id = $1 AND team_kind = 'nhl'
ORDER BY season DESC;

-- name: UpsertSeasonTeam :exec
-- Upsert an NHL season-team row. team_kind is implicitly 'nhl' (column
-- default); international teams use UpsertInternationalSeasonTeam.
INSERT INTO season_teams (
    season, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (season, team_id) DO UPDATE SET
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

-- name: UpsertInternationalSeasonTeam :exec
-- Upsert an international/national-team season row (Canada, USA,
-- Sweden, etc. at NHL API team IDs 60–67) referenced by
-- player_season_totals for WJC/Olympic/WC entries. franchise_id and
-- division/conference fields are NULL for these.
INSERT INTO season_teams (
    season, team_id, full_name, abbrev, team_kind
)
VALUES ($1, $2, $3, $4, 'international')
ON CONFLICT (season, team_id) DO UPDATE SET
    full_name = EXCLUDED.full_name,
    abbrev = EXCLUDED.abbrev
WHERE season_teams.team_kind = 'international'
  AND (season_teams.full_name, season_teams.abbrev)
      IS DISTINCT FROM (EXCLUDED.full_name, EXCLUDED.abbrev);

-- name: CountSeasonTeamsForSeason :one
SELECT COUNT(*) FROM season_teams WHERE season = $1;

-- name: CountSeasonTeams :one
SELECT COUNT(*) FROM season_teams;

-- name: GetDistinctDivisions :many
SELECT DISTINCT division_name, division_abbrev, conference_name, conference_abbrev
FROM season_teams
WHERE season = $1 AND team_kind = 'nhl'
ORDER BY conference_name, division_name;

-- name: ListTeamLogos :many
-- DISTINCT ON (team_id) with ORDER BY team_id, season DESC picks the latest-season
-- URL per team. Path layout is assets/teams/logos/<team_id>.<ext> (no per-season
-- nesting), so we want the newest URL to write.
SELECT DISTINCT ON (team_id) team_id, logo_url
FROM season_teams
WHERE logo_url IS NOT NULL AND logo_url <> ''
ORDER BY team_id, season DESC;

-- name: CountTeamLogos :one
-- Distinct teams that have at least one season with a non-empty logo URL.
-- Matches the cardinality of ListTeamLogos so the parent's progress bar Total
-- aligns with the rows the loader produces.
SELECT COUNT(DISTINCT team_id) FROM season_teams
WHERE logo_url IS NOT NULL AND logo_url <> '';
