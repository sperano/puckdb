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

-- name: UpsertYahooTeamStandingBatch :batchexec
-- Writes the standings snapshot of one team and date, creating the summary
-- row when the team's stats of that date were not imported. Only the
-- standings columns change, so the category columns are left as they are.
INSERT INTO yahoo_team_summaries (
    league_id, team_id, date,
    standings_captured_at, standings_rank, standings_playoff_seed,
    h2h_wins, h2h_losses, h2h_ties, h2h_percentage,
    h2h_division_wins, h2h_division_losses, h2h_division_ties, h2h_division_percentage
)
VALUES ($1, $2, $3,
    $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14)
ON CONFLICT (league_id, team_id, date) DO UPDATE SET
    standings_captured_at = EXCLUDED.standings_captured_at,
    standings_rank = EXCLUDED.standings_rank,
    standings_playoff_seed = EXCLUDED.standings_playoff_seed,
    h2h_wins = EXCLUDED.h2h_wins,
    h2h_losses = EXCLUDED.h2h_losses,
    h2h_ties = EXCLUDED.h2h_ties,
    h2h_percentage = EXCLUDED.h2h_percentage,
    h2h_division_wins = EXCLUDED.h2h_division_wins,
    h2h_division_losses = EXCLUDED.h2h_division_losses,
    h2h_division_ties = EXCLUDED.h2h_division_ties,
    h2h_division_percentage = EXCLUDED.h2h_division_percentage,
    updated_at = NOW()
WHERE (yahoo_team_summaries.standings_captured_at, yahoo_team_summaries.standings_rank,
       yahoo_team_summaries.standings_playoff_seed,
       yahoo_team_summaries.h2h_wins, yahoo_team_summaries.h2h_losses,
       yahoo_team_summaries.h2h_ties, yahoo_team_summaries.h2h_percentage,
       yahoo_team_summaries.h2h_division_wins, yahoo_team_summaries.h2h_division_losses,
       yahoo_team_summaries.h2h_division_ties, yahoo_team_summaries.h2h_division_percentage)
      IS DISTINCT FROM
      (EXCLUDED.standings_captured_at, EXCLUDED.standings_rank,
       EXCLUDED.standings_playoff_seed,
       EXCLUDED.h2h_wins, EXCLUDED.h2h_losses,
       EXCLUDED.h2h_ties, EXCLUDED.h2h_percentage,
       EXCLUDED.h2h_division_wins, EXCLUDED.h2h_division_losses,
       EXCLUDED.h2h_division_ties, EXCLUDED.h2h_division_percentage);

-- name: ClearYahooTeamStandingsExcept :exec
-- Removes the standings snapshot of a league's date from every team but
-- team_ids (the teams the current snapshot file wrote), so a team the file no
-- longer lists keeps no standing from an earlier file.
UPDATE yahoo_team_summaries SET
    standings_captured_at = NULL,
    standings_rank = NULL,
    standings_playoff_seed = NULL,
    h2h_wins = NULL,
    h2h_losses = NULL,
    h2h_ties = NULL,
    h2h_percentage = NULL,
    h2h_division_wins = NULL,
    h2h_division_losses = NULL,
    h2h_division_ties = NULL,
    h2h_division_percentage = NULL,
    updated_at = NOW()
WHERE league_id = sqlc.arg(league_id)
  AND date = sqlc.arg(date)
  AND standings_captured_at IS NOT NULL
  AND team_id <> ALL(sqlc.arg(team_ids)::integer[]);

-- name: GetLatestYahooStandingsSnapshotDate :one
-- The latest date of the league with a standings snapshot; NULL when the
-- league has none. Only this league's rows count, so another league's later
-- capture never moves it.
SELECT MAX(date)::date AS date
FROM yahoo_team_summaries
WHERE league_id = $1 AND standings_captured_at IS NOT NULL;

-- name: GetYahooTeamSummarySnapshot :many
-- A league's summary rows of one date, every team or only team_id: the
-- standings snapshot captured that date (NULL standings columns when none
-- was) and the fixed category columns. Ordered by team.
SELECT * FROM yahoo_team_summaries
WHERE league_id = sqlc.arg(league_id)
  AND date = sqlc.arg(date)
  AND (sqlc.narg(team_id)::integer IS NULL OR team_id = sqlc.narg(team_id))
ORDER BY team_id;

-- =============================================================================
-- Yahoo Team Summary Stats
-- =============================================================================

-- name: ReplaceYahooTeamSummaryStatsBatch :batchexec
-- Replaces the category values of one team summary with stats, a JSON array
-- of {"stat_id", "value", "raw_value"} objects with distinct stat IDs: values
-- the array lists are upserted, every other stat of the summary is deleted.
-- One statement, so a reader never sees half of a replacement.
WITH incoming AS (
    SELECT (e ->> 'stat_id')::integer AS stat_id,
           (e ->> 'value')::double precision AS value,
           (e ->> 'raw_value')::text AS raw_value
    FROM jsonb_array_elements(sqlc.arg(stats)::jsonb) AS e
), removed AS (
    DELETE FROM yahoo_team_summary_stats old
    WHERE old.league_id = sqlc.arg(league_id)::integer
      AND old.team_id = sqlc.arg(team_id)::integer
      AND old.date = sqlc.arg(date)::date
      AND old.stat_id NOT IN (SELECT incoming.stat_id FROM incoming)
)
INSERT INTO yahoo_team_summary_stats (league_id, team_id, date, stat_id, value, raw_value)
SELECT sqlc.arg(league_id)::integer, sqlc.arg(team_id)::integer, sqlc.arg(date)::date,
       incoming.stat_id, incoming.value, incoming.raw_value
FROM incoming
ON CONFLICT (league_id, team_id, date, stat_id) DO UPDATE SET
    value = EXCLUDED.value,
    raw_value = EXCLUDED.raw_value,
    updated_at = NOW()
WHERE (yahoo_team_summary_stats.value, yahoo_team_summary_stats.raw_value)
      IS DISTINCT FROM (EXCLUDED.value, EXCLUDED.raw_value);

-- name: GetYahooTeamSummaryStatsSnapshot :many
-- A league's category values of one date, every team or only team_id, with
-- the league's category definition when it has one (NULL columns for a stat
-- ID the league settings do not list). Ordered by team, then stat ID.
SELECT s.team_id,
       s.stat_id,
       s.value,
       s.raw_value,
       c.name,
       c.display_name,
       c.abbr,
       c.position_type,
       c.sort_order,
       c.is_only_display_stat
FROM yahoo_team_summary_stats s
LEFT JOIN yahoo_league_stat_categories c
    ON c.league_id = s.league_id AND c.stat_id = s.stat_id
WHERE s.league_id = sqlc.arg(league_id)
  AND s.date = sqlc.arg(date)
  AND (sqlc.narg(team_id)::integer IS NULL OR s.team_id = sqlc.narg(team_id))
ORDER BY s.team_id, s.stat_id;

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

-- name: GetYahooLeagueRostersWithPlayers :many
-- The rows of GetYahooTeamRostersByDate joined to the matched NHL player.
-- name is the NHL player's, else Yahoo's from the league's imported pool
-- (yahoo_team_rosters stores no name), else empty.
SELECT r.team_id,
       r.selected_position,
       r.player_id AS yahoo_player_id,
       r.player_key,
       COALESCE(p.first_name || ' ' || p.last_name, lp.full_name, '')::text AS name,
       p.id AS nhl_player_id,
       r.editorial_team_abbr,
       r.display_position,
       r.player_status,
       r.injury_note
FROM yahoo_team_rosters r
JOIN yahoo_leagues l ON l.id = r.league_id
LEFT JOIN players p ON p.yahoo_id = r.player_id
LEFT JOIN yahoo_league_players lp ON lp.league_key = l.league_key AND lp.player_id = r.player_id
WHERE r.league_id = $1 AND r.date = $2
ORDER BY r.team_id, r.selected_position, r.player_id;

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
