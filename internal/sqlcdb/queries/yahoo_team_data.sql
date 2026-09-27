-- =============================================================================
-- Yahoo Team Summaries
-- =============================================================================

-- name: GetYahooTeamSummary :one
SELECT * FROM yahoo_team_summaries
WHERE league_id = $1 AND team_id = $2 AND date = $3;

-- name: GetYahooTeamSummariesByLeague :many
SELECT * FROM yahoo_team_summaries
WHERE league_id = $1
ORDER BY team_id, date;

-- name: GetYahooTeamSummariesByDate :many
SELECT * FROM yahoo_team_summaries
WHERE league_id = $1 AND date = $2
ORDER BY team_id;

-- name: GetYahooTeamSummariesByTeam :many
SELECT * FROM yahoo_team_summaries
WHERE league_id = $1 AND team_id = $2
ORDER BY date;

-- name: CountYahooTeamSummaries :one
SELECT COUNT(*) FROM yahoo_team_summaries;

-- name: CountYahooTeamSummariesByLeague :one
SELECT COUNT(*) FROM yahoo_team_summaries WHERE league_id = $1;

-- name: DeleteYahooTeamSummary :exec
DELETE FROM yahoo_team_summaries
WHERE league_id = $1 AND team_id = $2 AND date = $3;

-- name: DeleteYahooTeamSummariesByLeague :exec
DELETE FROM yahoo_team_summaries WHERE league_id = $1;

-- name: DeleteYahooTeamSummariesByDate :exec
DELETE FROM yahoo_team_summaries WHERE league_id = $1 AND date = $2;

-- name: UpsertYahooTeamSummaryBatch :batchexec
INSERT INTO yahoo_team_summaries (
    league_id, team_id, date, coverage_type,
    goals, assists, points, plus_minus, pim, ppp, sog,
    faceoffs_won, faceoffs_lost,
    wins, goals_against, gaa, shots_against, saves, save_pct, shutouts,
    shp, gwg, hits, blocks
)
VALUES ($1, $2, $3, $4,
    $5, $6, $7, $8, $9, $10, $11,
    $12, $13,
    $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24)
ON CONFLICT (league_id, team_id, date) DO UPDATE SET
    coverage_type = EXCLUDED.coverage_type,
    goals = EXCLUDED.goals,
    assists = EXCLUDED.assists,
    points = EXCLUDED.points,
    plus_minus = EXCLUDED.plus_minus,
    pim = EXCLUDED.pim,
    ppp = EXCLUDED.ppp,
    sog = EXCLUDED.sog,
    faceoffs_won = EXCLUDED.faceoffs_won,
    faceoffs_lost = EXCLUDED.faceoffs_lost,
    wins = EXCLUDED.wins,
    goals_against = EXCLUDED.goals_against,
    gaa = EXCLUDED.gaa,
    shots_against = EXCLUDED.shots_against,
    saves = EXCLUDED.saves,
    save_pct = EXCLUDED.save_pct,
    shutouts = EXCLUDED.shutouts,
    shp = EXCLUDED.shp,
    gwg = EXCLUDED.gwg,
    hits = EXCLUDED.hits,
    blocks = EXCLUDED.blocks,
    updated_at = NOW()
WHERE (yahoo_team_summaries.coverage_type,
       yahoo_team_summaries.goals, yahoo_team_summaries.assists,
       yahoo_team_summaries.points, yahoo_team_summaries.plus_minus,
       yahoo_team_summaries.pim, yahoo_team_summaries.ppp,
       yahoo_team_summaries.sog, yahoo_team_summaries.faceoffs_won,
       yahoo_team_summaries.faceoffs_lost, yahoo_team_summaries.wins,
       yahoo_team_summaries.goals_against, yahoo_team_summaries.gaa,
       yahoo_team_summaries.shots_against, yahoo_team_summaries.saves,
       yahoo_team_summaries.save_pct, yahoo_team_summaries.shutouts,
       yahoo_team_summaries.shp, yahoo_team_summaries.gwg,
       yahoo_team_summaries.hits, yahoo_team_summaries.blocks)
      IS DISTINCT FROM
      (EXCLUDED.coverage_type,
       EXCLUDED.goals, EXCLUDED.assists,
       EXCLUDED.points, EXCLUDED.plus_minus,
       EXCLUDED.pim, EXCLUDED.ppp,
       EXCLUDED.sog, EXCLUDED.faceoffs_won,
       EXCLUDED.faceoffs_lost, EXCLUDED.wins,
       EXCLUDED.goals_against, EXCLUDED.gaa,
       EXCLUDED.shots_against, EXCLUDED.saves,
       EXCLUDED.save_pct, EXCLUDED.shutouts,
       EXCLUDED.shp, EXCLUDED.gwg,
       EXCLUDED.hits, EXCLUDED.blocks);

-- =============================================================================
-- Yahoo Team Rosters
-- =============================================================================

-- name: GetYahooTeamRoster :many
SELECT * FROM yahoo_team_rosters
WHERE league_id = $1 AND team_id = $2 AND date = $3
ORDER BY player_id;

-- name: GetYahooTeamRostersByLeague :many
SELECT * FROM yahoo_team_rosters
WHERE league_id = $1
ORDER BY team_id, date, player_id;

-- name: GetYahooTeamRostersByDate :many
SELECT * FROM yahoo_team_rosters
WHERE league_id = $1 AND date = $2
ORDER BY team_id, player_id;

-- name: GetYahooTeamRostersByPlayer :many
SELECT * FROM yahoo_team_rosters
WHERE league_id = $1 AND player_id = $2
ORDER BY date;

-- name: GetYahooTeamRostersByTeam :many
SELECT * FROM yahoo_team_rosters
WHERE league_id = $1 AND team_id = $2
ORDER BY date, player_id;

-- name: CountYahooTeamRosters :one
SELECT COUNT(*) FROM yahoo_team_rosters;

-- name: CountYahooTeamRostersByLeague :one
SELECT COUNT(*) FROM yahoo_team_rosters WHERE league_id = $1;

-- name: CountYahooTeamRostersByDate :one
SELECT COUNT(*) FROM yahoo_team_rosters WHERE league_id = $1 AND date = $2;

-- name: DeleteYahooTeamRoster :exec
DELETE FROM yahoo_team_rosters
WHERE league_id = $1 AND team_id = $2 AND date = $3;

-- name: DeleteYahooTeamRostersByLeague :exec
DELETE FROM yahoo_team_rosters WHERE league_id = $1;

-- name: DeleteYahooTeamRostersByDate :exec
DELETE FROM yahoo_team_rosters WHERE league_id = $1 AND date = $2;

-- name: UpsertYahooTeamRosterBatch :batchexec
INSERT INTO yahoo_team_rosters (
    league_id, team_id, date, player_id,
    coverage_type, is_editable, player_key,
    selected_position, is_flex,
    player_status, player_status_full, injury_note, on_disabled_list,
    position_type, display_position, primary_position, eligible_positions,
    uniform_number, editorial_team_abbr
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
ON CONFLICT (league_id, team_id, date, player_id) DO UPDATE SET
    coverage_type = EXCLUDED.coverage_type,
    is_editable = EXCLUDED.is_editable,
    player_key = EXCLUDED.player_key,
    selected_position = EXCLUDED.selected_position,
    is_flex = EXCLUDED.is_flex,
    player_status = EXCLUDED.player_status,
    player_status_full = EXCLUDED.player_status_full,
    injury_note = EXCLUDED.injury_note,
    on_disabled_list = EXCLUDED.on_disabled_list,
    position_type = EXCLUDED.position_type,
    display_position = EXCLUDED.display_position,
    primary_position = EXCLUDED.primary_position,
    eligible_positions = EXCLUDED.eligible_positions,
    uniform_number = EXCLUDED.uniform_number,
    editorial_team_abbr = EXCLUDED.editorial_team_abbr,
    updated_at = NOW()
WHERE (yahoo_team_rosters.coverage_type, yahoo_team_rosters.is_editable,
       yahoo_team_rosters.player_key, yahoo_team_rosters.selected_position,
       yahoo_team_rosters.is_flex,
       yahoo_team_rosters.player_status, yahoo_team_rosters.player_status_full,
       yahoo_team_rosters.injury_note, yahoo_team_rosters.on_disabled_list,
       yahoo_team_rosters.position_type, yahoo_team_rosters.display_position,
       yahoo_team_rosters.primary_position, yahoo_team_rosters.eligible_positions,
       yahoo_team_rosters.uniform_number, yahoo_team_rosters.editorial_team_abbr)
      IS DISTINCT FROM
      (EXCLUDED.coverage_type, EXCLUDED.is_editable,
       EXCLUDED.player_key, EXCLUDED.selected_position,
       EXCLUDED.is_flex,
       EXCLUDED.player_status, EXCLUDED.player_status_full,
       EXCLUDED.injury_note, EXCLUDED.on_disabled_list,
       EXCLUDED.position_type, EXCLUDED.display_position,
       EXCLUDED.primary_position, EXCLUDED.eligible_positions,
       EXCLUDED.uniform_number, EXCLUDED.editorial_team_abbr);

-- name: ListLatestYahooEligiblePositionsByPlayer :many
-- TEMPORARY: backs the stand-in draft pool's Yahoo eligible-position lookup
-- (internal/draft/standin_pool.go, config.LeagueMetadataSource). For every
-- NHL player with a known Yahoo id, returns the raw eligible_positions
-- (Yahoo position codes, including non-draftable ones like Util/IR) from
-- their single most recent yahoo_team_rosters row across all leagues and
-- teams; the caller filters and reorders them. Remove together with
-- temporary_metadata_from once Yahoo access returns.
SELECT DISTINCT ON (p.id)
    p.id AS player_id,
    ytr.eligible_positions
FROM players p
JOIN yahoo_team_rosters ytr ON ytr.player_id = p.yahoo_id
WHERE p.yahoo_id IS NOT NULL
ORDER BY p.id, ytr.date DESC;
