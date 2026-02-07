-- name: GetAllNHLConferences :many
SELECT * FROM nhl_conferences ORDER BY id;

-- name: GetNHLConference :one
SELECT * FROM nhl_conferences WHERE id = $1;

-- name: GetAllNHLDivisions :many
SELECT
    d.id, d.name, d.nhl_conference_id,
    c.id AS conf_id, c.name AS conf_name
FROM nhl_divisions d
JOIN nhl_conferences c ON d.nhl_conference_id = c.id
ORDER BY d.id;

-- name: GetNHLDivisionsByConference :many
SELECT
    d.id, d.name, d.nhl_conference_id,
    c.id AS conf_id, c.name AS conf_name
FROM nhl_divisions d
JOIN nhl_conferences c ON d.nhl_conference_id = c.id
WHERE d.nhl_conference_id = $1
ORDER BY d.id;

-- name: GetAllNHLTeams :many
SELECT
    t.id, t.yahoo_id, t.city, t.name, t.abbreviation, t.nhl_division_id,
    t.nhl_home_link, t.yahoo_home_link, t.small_logo_url, t.large_logo_url, t.all_stars,
    d.id AS div_id, d.name AS div_name,
    c.id AS conf_id, c.name AS conf_name
FROM nhl_teams t
JOIN nhl_divisions d ON t.nhl_division_id = d.id
JOIN nhl_conferences c ON d.nhl_conference_id = c.id
WHERE t.all_stars = $1
ORDER BY t.id;

-- name: GetNHLTeam :one
SELECT
    t.id, t.yahoo_id, t.city, t.name, t.abbreviation, t.nhl_division_id,
    t.nhl_home_link, t.yahoo_home_link, t.small_logo_url, t.large_logo_url, t.all_stars,
    d.id AS div_id, d.name AS div_name,
    c.id AS conf_id, c.name AS conf_name
FROM nhl_teams t
JOIN nhl_divisions d ON t.nhl_division_id = d.id
JOIN nhl_conferences c ON d.nhl_conference_id = c.id
WHERE t.id = $1;

-- name: GetNHLTeamsByDivision :many
SELECT
    t.id, t.yahoo_id, t.city, t.name, t.abbreviation, t.nhl_division_id,
    t.nhl_home_link, t.yahoo_home_link, t.small_logo_url, t.large_logo_url, t.all_stars,
    d.id AS div_id, d.name AS div_name,
    c.id AS conf_id, c.name AS conf_name
FROM nhl_teams t
JOIN nhl_divisions d ON t.nhl_division_id = d.id
JOIN nhl_conferences c ON d.nhl_conference_id = c.id
WHERE t.nhl_division_id = $1
ORDER BY t.id;

-- name: UpsertNHLConference :exec
INSERT INTO nhl_conferences (id, name)
VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

-- name: UpsertNHLDivision :exec
INSERT INTO nhl_divisions (id, name, nhl_conference_id)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    nhl_conference_id = EXCLUDED.nhl_conference_id;

-- name: UpsertNHLTeam :exec
INSERT INTO nhl_teams (id, yahoo_id, city, name, abbreviation, nhl_division_id, nhl_home_link, yahoo_home_link, small_logo_url, large_logo_url, all_stars)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (id) DO UPDATE SET
    yahoo_id = EXCLUDED.yahoo_id,
    city = EXCLUDED.city,
    name = EXCLUDED.name,
    abbreviation = EXCLUDED.abbreviation,
    nhl_division_id = EXCLUDED.nhl_division_id,
    nhl_home_link = EXCLUDED.nhl_home_link,
    yahoo_home_link = EXCLUDED.yahoo_home_link,
    small_logo_url = EXCLUDED.small_logo_url,
    large_logo_url = EXCLUDED.large_logo_url,
    all_stars = EXCLUDED.all_stars;

-- name: GetAllNHLTeamIDs :many
SELECT id FROM nhl_teams ORDER BY id;
