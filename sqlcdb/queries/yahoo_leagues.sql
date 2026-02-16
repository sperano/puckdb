-- =============================================================================
-- Yahoo Leagues
-- =============================================================================

-- name: GetYahooLeague :one
SELECT * FROM yahoo_leagues WHERE id = $1;

-- name: GetYahooLeagueByKey :one
SELECT * FROM yahoo_leagues WHERE league_key = $1;

-- name: GetYahooLeaguesBySeason :many
SELECT * FROM yahoo_leagues WHERE season = $1 ORDER BY name;

-- name: GetAllYahooLeagues :many
SELECT * FROM yahoo_leagues ORDER BY season DESC, name;

-- name: UpsertYahooLeague :exec
INSERT INTO yahoo_leagues (
    id, league_key, name, url, logo_url,
    season, game_code, num_teams, scoring_type, league_type, draft_status,
    is_pro_league, is_cash_league, start_date, end_date,
    draft_type, is_auction_draft, draft_time, draft_pick_time,
    waiver_type, waiver_rule, waiver_time,
    trade_end_date, trade_ratify_type, trade_reject_time,
    max_teams, player_pool, post_draft_players, cant_cut_list,
    uses_playoff, persistent_url, league_update_timestamp
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15,
    $16, $17, $18, $19,
    $20, $21, $22,
    $23, $24, $25,
    $26, $27, $28, $29,
    $30, $31, $32
)
ON CONFLICT (id) DO UPDATE SET
    league_key = EXCLUDED.league_key,
    name = EXCLUDED.name,
    url = EXCLUDED.url,
    logo_url = EXCLUDED.logo_url,
    season = EXCLUDED.season,
    game_code = EXCLUDED.game_code,
    num_teams = EXCLUDED.num_teams,
    scoring_type = EXCLUDED.scoring_type,
    league_type = EXCLUDED.league_type,
    draft_status = EXCLUDED.draft_status,
    is_pro_league = EXCLUDED.is_pro_league,
    is_cash_league = EXCLUDED.is_cash_league,
    start_date = EXCLUDED.start_date,
    end_date = EXCLUDED.end_date,
    draft_type = EXCLUDED.draft_type,
    is_auction_draft = EXCLUDED.is_auction_draft,
    draft_time = EXCLUDED.draft_time,
    draft_pick_time = EXCLUDED.draft_pick_time,
    waiver_type = EXCLUDED.waiver_type,
    waiver_rule = EXCLUDED.waiver_rule,
    waiver_time = EXCLUDED.waiver_time,
    trade_end_date = EXCLUDED.trade_end_date,
    trade_ratify_type = EXCLUDED.trade_ratify_type,
    trade_reject_time = EXCLUDED.trade_reject_time,
    max_teams = EXCLUDED.max_teams,
    player_pool = EXCLUDED.player_pool,
    post_draft_players = EXCLUDED.post_draft_players,
    cant_cut_list = EXCLUDED.cant_cut_list,
    uses_playoff = EXCLUDED.uses_playoff,
    persistent_url = EXCLUDED.persistent_url,
    league_update_timestamp = EXCLUDED.league_update_timestamp,
    updated_at = NOW();

-- name: CountYahooLeagues :one
SELECT COUNT(*) FROM yahoo_leagues;

-- name: CountYahooLeaguesBySeason :one
SELECT COUNT(*) FROM yahoo_leagues WHERE season = $1;

-- name: DeleteYahooLeague :exec
DELETE FROM yahoo_leagues WHERE id = $1;

-- =============================================================================
-- Yahoo League Roster Positions
-- =============================================================================

-- name: GetYahooLeagueRosterPositions :many
SELECT * FROM yahoo_league_roster_positions
WHERE league_id = $1
ORDER BY is_starting_position DESC, position;

-- name: GetYahooLeagueStartingPositions :many
SELECT * FROM yahoo_league_roster_positions
WHERE league_id = $1 AND is_starting_position = TRUE
ORDER BY position;

-- name: DeleteYahooLeagueRosterPositions :exec
DELETE FROM yahoo_league_roster_positions WHERE league_id = $1;

-- name: UpsertYahooLeagueRosterPositionBatch :batchexec
INSERT INTO yahoo_league_roster_positions (
    league_id, position, position_type, count, is_starting_position
)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (league_id, position) DO UPDATE SET
    position_type = EXCLUDED.position_type,
    count = EXCLUDED.count,
    is_starting_position = EXCLUDED.is_starting_position;

-- =============================================================================
-- Yahoo League Stat Categories
-- =============================================================================

-- name: GetYahooLeagueStatCategories :many
SELECT * FROM yahoo_league_stat_categories
WHERE league_id = $1
ORDER BY stat_group, name;

-- name: GetYahooLeagueEnabledStatCategories :many
SELECT * FROM yahoo_league_stat_categories
WHERE league_id = $1 AND enabled = TRUE
ORDER BY stat_group, name;

-- name: DeleteYahooLeagueStatCategories :exec
DELETE FROM yahoo_league_stat_categories WHERE league_id = $1;

-- name: UpsertYahooLeagueStatCategoryBatch :batchexec
INSERT INTO yahoo_league_stat_categories (
    league_id, stat_id, name, abbr, stat_group, enabled, value
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (league_id, stat_id) DO UPDATE SET
    name = EXCLUDED.name,
    abbr = EXCLUDED.abbr,
    stat_group = EXCLUDED.stat_group,
    enabled = EXCLUDED.enabled,
    value = EXCLUDED.value;

-- =============================================================================
-- Yahoo Teams
-- =============================================================================

-- name: GetYahooTeam :one
SELECT * FROM yahoo_teams WHERE league_id = $1 AND id = $2;

-- name: GetYahooTeamByKey :one
SELECT * FROM yahoo_teams WHERE team_key = $1;

-- name: GetYahooTeamsByLeague :many
SELECT * FROM yahoo_teams WHERE league_id = $1 ORDER BY id;

-- name: GetYahooTeamsByLeagueWithWaiverOrder :many
SELECT * FROM yahoo_teams WHERE league_id = $1 ORDER BY waiver_priority;

-- name: CountYahooTeams :one
SELECT COUNT(*) FROM yahoo_teams;

-- name: CountYahooTeamsByLeague :one
SELECT COUNT(*) FROM yahoo_teams WHERE league_id = $1;

-- name: DeleteYahooTeam :exec
DELETE FROM yahoo_teams WHERE league_id = $1 AND id = $2;

-- name: DeleteYahooTeamsByLeague :exec
DELETE FROM yahoo_teams WHERE league_id = $1;

-- name: UpsertYahooTeamBatch :batchexec
INSERT INTO yahoo_teams (
    league_id, id, team_key, name, url, logo_url,
    draft_position, waiver_priority, number_of_moves, number_of_trades,
    is_owned_by_current_login
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (league_id, id) DO UPDATE SET
    team_key = EXCLUDED.team_key,
    name = EXCLUDED.name,
    url = EXCLUDED.url,
    logo_url = EXCLUDED.logo_url,
    draft_position = EXCLUDED.draft_position,
    waiver_priority = EXCLUDED.waiver_priority,
    number_of_moves = EXCLUDED.number_of_moves,
    number_of_trades = EXCLUDED.number_of_trades,
    is_owned_by_current_login = EXCLUDED.is_owned_by_current_login,
    updated_at = NOW();

-- =============================================================================
-- Yahoo Team Managers
-- =============================================================================

-- name: GetYahooTeamManagers :many
SELECT * FROM yahoo_team_managers
WHERE league_id = $1 AND team_id = $2
ORDER BY id;

-- name: GetYahooTeamManagerByGUID :one
SELECT * FROM yahoo_team_managers WHERE guid = $1;

-- name: DeleteYahooTeamManagers :exec
DELETE FROM yahoo_team_managers WHERE league_id = $1 AND team_id = $2;

-- name: DeleteYahooTeamManagersByLeague :exec
DELETE FROM yahoo_team_managers WHERE league_id = $1;

-- name: UpsertYahooTeamManagerBatch :batchexec
INSERT INTO yahoo_team_managers (
    league_id, team_id, id, nickname, guid, email, image_url,
    felo_score, felo_tier, is_current_login, is_commissioner
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (league_id, team_id, id) DO UPDATE SET
    nickname = EXCLUDED.nickname,
    guid = EXCLUDED.guid,
    email = EXCLUDED.email,
    image_url = EXCLUDED.image_url,
    felo_score = EXCLUDED.felo_score,
    felo_tier = EXCLUDED.felo_tier,
    is_current_login = EXCLUDED.is_current_login,
    is_commissioner = EXCLUDED.is_commissioner;
