-- =============================================================================
-- Season Series Data Queries (Officials, Coaches, Scratches)
-- =============================================================================

-- name: UpsertGameOfficial :exec
INSERT INTO game_officials (game_id, role, sequence, name)
VALUES ($1, $2, $3, $4)
ON CONFLICT (game_id, role, sequence) DO UPDATE SET
    name = EXCLUDED.name;

-- name: UpsertGameCoach :exec
INSERT INTO game_coaches (game_id, team_id, head_coach)
VALUES ($1, $2, $3)
ON CONFLICT (game_id, team_id) DO UPDATE SET
    head_coach = EXCLUDED.head_coach;

-- name: UpsertGameScratch :exec
INSERT INTO game_scratches (game_id, team_id, player_id)
VALUES ($1, $2, $3)
ON CONFLICT (game_id, player_id) DO UPDATE SET
    team_id = EXCLUDED.team_id;

-- name: GetGameOfficials :many
SELECT game_id, role, sequence, name
FROM game_officials
WHERE game_id = $1
ORDER BY role, sequence;

-- name: GetGameCoaches :many
SELECT gc.game_id, gc.team_id, gc.head_coach, st.abbrev as team_abbrev
FROM game_coaches gc
JOIN games g ON gc.game_id = g.id
JOIN season_teams st ON gc.team_id = st.team_id AND g.season = st.season_id
WHERE gc.game_id = $1;

-- name: GetGameScratches :many
SELECT gs.game_id, gs.team_id, gs.player_id, p.first_name, p.last_name, p.position
FROM game_scratches gs
JOIN players p ON gs.player_id = p.id
WHERE gs.game_id = $1
ORDER BY gs.team_id, p.last_name;
