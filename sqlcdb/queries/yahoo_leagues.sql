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
    updated_at = NOW()
WHERE (yahoo_leagues.league_key, yahoo_leagues.name, yahoo_leagues.url,
       yahoo_leagues.logo_url, yahoo_leagues.season,
       yahoo_leagues.game_code, yahoo_leagues.num_teams,
       yahoo_leagues.scoring_type, yahoo_leagues.league_type,
       yahoo_leagues.draft_status, yahoo_leagues.is_pro_league,
       yahoo_leagues.is_cash_league, yahoo_leagues.start_date,
       yahoo_leagues.end_date, yahoo_leagues.draft_type,
       yahoo_leagues.is_auction_draft, yahoo_leagues.draft_time,
       yahoo_leagues.draft_pick_time, yahoo_leagues.waiver_type,
       yahoo_leagues.waiver_rule, yahoo_leagues.waiver_time,
       yahoo_leagues.trade_end_date, yahoo_leagues.trade_ratify_type,
       yahoo_leagues.trade_reject_time, yahoo_leagues.max_teams,
       yahoo_leagues.player_pool, yahoo_leagues.post_draft_players,
       yahoo_leagues.cant_cut_list, yahoo_leagues.uses_playoff,
       yahoo_leagues.persistent_url, yahoo_leagues.league_update_timestamp)
      IS DISTINCT FROM
      (EXCLUDED.league_key, EXCLUDED.name, EXCLUDED.url,
       EXCLUDED.logo_url, EXCLUDED.season,
       EXCLUDED.game_code, EXCLUDED.num_teams,
       EXCLUDED.scoring_type, EXCLUDED.league_type,
       EXCLUDED.draft_status, EXCLUDED.is_pro_league,
       EXCLUDED.is_cash_league, EXCLUDED.start_date,
       EXCLUDED.end_date, EXCLUDED.draft_type,
       EXCLUDED.is_auction_draft, EXCLUDED.draft_time,
       EXCLUDED.draft_pick_time, EXCLUDED.waiver_type,
       EXCLUDED.waiver_rule, EXCLUDED.waiver_time,
       EXCLUDED.trade_end_date, EXCLUDED.trade_ratify_type,
       EXCLUDED.trade_reject_time, EXCLUDED.max_teams,
       EXCLUDED.player_pool, EXCLUDED.post_draft_players,
       EXCLUDED.cant_cut_list, EXCLUDED.uses_playoff,
       EXCLUDED.persistent_url, EXCLUDED.league_update_timestamp);

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
    is_starting_position = EXCLUDED.is_starting_position
WHERE (yahoo_league_roster_positions.position_type,
       yahoo_league_roster_positions.count,
       yahoo_league_roster_positions.is_starting_position)
      IS DISTINCT FROM
      (EXCLUDED.position_type, EXCLUDED.count, EXCLUDED.is_starting_position);

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
    value = EXCLUDED.value
WHERE (yahoo_league_stat_categories.name, yahoo_league_stat_categories.abbr,
       yahoo_league_stat_categories.stat_group,
       yahoo_league_stat_categories.enabled,
       yahoo_league_stat_categories.value)
      IS DISTINCT FROM
      (EXCLUDED.name, EXCLUDED.abbr, EXCLUDED.stat_group,
       EXCLUDED.enabled, EXCLUDED.value);

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
    updated_at = NOW()
WHERE (yahoo_teams.team_key, yahoo_teams.name, yahoo_teams.url,
       yahoo_teams.logo_url, yahoo_teams.draft_position,
       yahoo_teams.waiver_priority, yahoo_teams.number_of_moves,
       yahoo_teams.number_of_trades, yahoo_teams.is_owned_by_current_login)
      IS DISTINCT FROM
      (EXCLUDED.team_key, EXCLUDED.name, EXCLUDED.url,
       EXCLUDED.logo_url, EXCLUDED.draft_position,
       EXCLUDED.waiver_priority, EXCLUDED.number_of_moves,
       EXCLUDED.number_of_trades, EXCLUDED.is_owned_by_current_login);

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
    is_commissioner = EXCLUDED.is_commissioner
WHERE (yahoo_team_managers.nickname, yahoo_team_managers.guid,
       yahoo_team_managers.email, yahoo_team_managers.image_url,
       yahoo_team_managers.felo_score, yahoo_team_managers.felo_tier,
       yahoo_team_managers.is_current_login, yahoo_team_managers.is_commissioner)
      IS DISTINCT FROM
      (EXCLUDED.nickname, EXCLUDED.guid,
       EXCLUDED.email, EXCLUDED.image_url,
       EXCLUDED.felo_score, EXCLUDED.felo_tier,
       EXCLUDED.is_current_login, EXCLUDED.is_commissioner);
