-- =============================================================================
-- Yahoo League Rule Snapshots (versioned league settings for the draft helper)
-- =============================================================================

-- name: UpsertYahooLeagueRuleSnapshot :one
-- Inserts a new rules version, or marks an identical version as seen again.
-- inserted is true when this call created the version.
INSERT INTO yahoo_league_rule_snapshots (
    season, league_id, league_key, game_key, source,
    source_season, source_league_key, fetched_at, rules_hash, rules
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (season, league_key, rules_hash) DO UPDATE SET
    fetched_at = GREATEST(yahoo_league_rule_snapshots.fetched_at, EXCLUDED.fetched_at),
    last_seen_at = NOW()
RETURNING id, (xmax = 0)::boolean AS inserted;

-- name: GetLatestYahooLeagueRuleSnapshot :one
SELECT * FROM yahoo_league_rule_snapshots
WHERE season = $1 AND league_id = $2
ORDER BY last_seen_at DESC, id DESC
LIMIT 1;

-- name: CountYahooLeagueRuleSnapshots :one
SELECT COUNT(*) FROM yahoo_league_rule_snapshots
WHERE season = $1 AND league_id = $2;

-- name: GetYahooOwnedTeam :one
-- The logged-in user's team in a league, if teams were imported.
SELECT * FROM yahoo_teams
WHERE league_id = $1 AND is_owned_by_current_login = TRUE
ORDER BY id
LIMIT 1;
