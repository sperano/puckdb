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
    league_id, team_id, date, coverage_type
)
VALUES ($1, $2, $3, $4)
ON CONFLICT (league_id, team_id, date) DO UPDATE SET
    coverage_type = EXCLUDED.coverage_type,
    updated_at = NOW()
WHERE yahoo_team_summaries.coverage_type IS DISTINCT FROM EXCLUDED.coverage_type;

-- =============================================================================
-- Yahoo Team Summary Stats
-- =============================================================================

-- name: GetYahooTeamSummaryStats :many
SELECT * FROM yahoo_team_summary_stats
WHERE league_id = $1 AND team_id = $2 AND date = $3
ORDER BY stat_id;

-- name: GetYahooTeamSummaryStatsByLeagueAndDate :many
SELECT * FROM yahoo_team_summary_stats
WHERE league_id = $1 AND date = $2
ORDER BY team_id, stat_id;

-- name: GetYahooTeamSummaryStatsByStat :many
SELECT * FROM yahoo_team_summary_stats
WHERE league_id = $1 AND stat_id = $2
ORDER BY date, team_id;

-- name: CountYahooTeamSummaryStats :one
SELECT COUNT(*) FROM yahoo_team_summary_stats;

-- name: DeleteYahooTeamSummaryStats :exec
DELETE FROM yahoo_team_summary_stats
WHERE league_id = $1 AND team_id = $2 AND date = $3;

-- name: DeleteYahooTeamSummaryStatsByLeague :exec
DELETE FROM yahoo_team_summary_stats WHERE league_id = $1;

-- name: UpsertYahooTeamSummaryStatBatch :batchexec
INSERT INTO yahoo_team_summary_stats (
    league_id, team_id, date, stat_id, value
)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (league_id, team_id, date, stat_id) DO UPDATE SET
    value = EXCLUDED.value
WHERE yahoo_team_summary_stats.value IS DISTINCT FROM EXCLUDED.value;

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
