-- =============================================================================
-- Yahoo League Players (draftable pool with league eligibility and status)
-- =============================================================================

-- name: UpsertYahooLeaguePlayerBatch :batchexec
INSERT INTO yahoo_league_players (
    league_key, season, league_id, game_key, player_id, player_key,
    full_name, editorial_team_abbr, display_position, primary_position,
    position_type, eligible_positions, status, status_full, injury_note,
    on_disabled_list, fetched_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (league_key, player_id) DO UPDATE SET
    season = EXCLUDED.season,
    league_id = EXCLUDED.league_id,
    game_key = EXCLUDED.game_key,
    player_key = EXCLUDED.player_key,
    full_name = EXCLUDED.full_name,
    editorial_team_abbr = EXCLUDED.editorial_team_abbr,
    display_position = EXCLUDED.display_position,
    primary_position = EXCLUDED.primary_position,
    position_type = EXCLUDED.position_type,
    eligible_positions = EXCLUDED.eligible_positions,
    status = EXCLUDED.status,
    status_full = EXCLUDED.status_full,
    injury_note = EXCLUDED.injury_note,
    on_disabled_list = EXCLUDED.on_disabled_list,
    fetched_at = EXCLUDED.fetched_at,
    imported_at = NOW();

-- name: DeleteStaleYahooLeaguePlayers :execrows
-- Removes players a newer complete pool snapshot no longer lists.
DELETE FROM yahoo_league_players
WHERE league_key = $1 AND fetched_at < $2;

-- name: ListYahooLeaguePlayersWithNHL :many
-- The league's pool with the NHL player each Yahoo ID maps to (NULL when
-- unmatched, e.g. a rookie without NHL history).
SELECT lp.*, p.id AS nhl_player_id
FROM yahoo_league_players lp
LEFT JOIN players p ON p.yahoo_id = lp.player_id
WHERE lp.league_key = $1
ORDER BY lp.player_id;
