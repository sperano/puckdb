-- name: CreateNewsAdjustmentOverride :exec
INSERT INTO news_adjustment_overrides (
    id, player_key, league_key, kind, event_id, scenario, input, value,
    reason, created_by, created_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: GetNewsEventExclusionTarget :one
-- What storing an exclusion of news event @event_id must check: whether the
-- event is about the player (named by NHL ID, by Yahoo ID, or through the NHL
-- player matched to that Yahoo ID, the mapping a draft pool uses), and whether
-- its effect on other events is stored on those events (a reinstatement
-- resolves the absences it ends; another event names it as superseded_by). No
-- row when the event does not exist.
SELECT
    ((ev.nhl_player_id = sqlc.narg(nhl_player_id)::bigint
        OR ev.yahoo_player_id = sqlc.narg(yahoo_player_id)::integer
        OR ev.nhl_player_id IN (SELECT p.id FROM players p WHERE p.yahoo_id = sqlc.narg(yahoo_player_id)::integer)
    ) IS TRUE)::boolean AS about_player,
    (ev.event_type = sqlc.arg(reinstatement_type)::text)::boolean AS is_reinstatement,
    EXISTS (SELECT 1 FROM news_events o WHERE o.superseded_by = ev.id)::boolean AS supersedes_another
FROM news_events ev
WHERE ev.id = sqlc.arg(event_id)::bigint;

-- name: ResetNewsAdjustmentOverride :execrows
UPDATE news_adjustment_overrides
SET reset_at = $2, reset_reason = $3
WHERE id = $1 AND reset_at IS NULL AND created_at <= $2;

-- name: ListNewsAdjustmentOverrides :many
SELECT * FROM news_adjustment_overrides
ORDER BY created_at, id;

-- name: UpsertNewsAdjustmentRun :one
INSERT INTO news_adjustment_runs (
    adjustment_id, method_version, policy_hash, policy, baseline_snapshot_id,
    baseline_source_hash, league_key, as_of, season, overrides,
    shadowed_overrides, coverage_warnings
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (adjustment_id) DO UPDATE SET adjustment_id = EXCLUDED.adjustment_id
RETURNING id;

-- name: DeleteNewsAdjustmentEvents :exec
DELETE FROM news_adjustment_events WHERE run_id = $1;

-- name: DeleteNewsAdjustmentScenarios :exec
DELETE FROM news_adjustment_scenarios WHERE run_id = $1;

-- name: DeleteNewsAdjustmentPlayers :exec
DELETE FROM news_adjustment_players WHERE run_id = $1;

-- name: CreateNewsAdjustmentEvent :exec
INSERT INTO news_adjustment_events (
    run_id, event_id, version, player_key, incident_id, outcome, reason, scenarios, event
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: CreateNewsAdjustmentScenario :exec
INSERT INTO news_adjustment_scenarios (run_id, scenario, snapshot_id)
VALUES ($1, $2, $3);

-- name: CreateNewsAdjustmentPlayer :exec
INSERT INTO news_adjustment_players (run_id, player_key, adjustment)
VALUES ($1, $2, $3);

-- name: GetNewsAdjustmentRun :one
SELECT * FROM news_adjustment_runs WHERE id = $1;

-- name: ListNewsAdjustmentEvents :many
SELECT * FROM news_adjustment_events WHERE run_id = $1 ORDER BY player_key, event_id;

-- name: ListNewsAdjustmentScenarios :many
SELECT * FROM news_adjustment_scenarios WHERE run_id = $1 ORDER BY scenario;

-- name: ListNewsAdjustmentPlayers :many
SELECT * FROM news_adjustment_players WHERE run_id = $1 ORDER BY player_key;

-- name: ListNewsEventsForAdjustment :many
-- Every stored extraction event about the players, whatever its lifecycle,
-- with the extractor that created it.
SELECT sqlc.embed(ev), COALESCE(x.extractor_key, '')::text AS extractor_key
FROM news_events ev
LEFT JOIN news_extractions x ON x.id = ev.extraction_id
WHERE ev.nhl_player_id = ANY(@nhl_player_ids::bigint[])
   OR ev.yahoo_player_id = ANY(@yahoo_player_ids::integer[])
ORDER BY ev.id;

-- name: ListNewsEventAdjustmentEvidence :many
-- The evidence rows of the events, each with when it was recorded and the
-- article version's retrieval time and URL.
SELECT ee.event_id, ee.version_id, ee.extraction_id, ee.relation, ee.publisher, ee.kind,
       ee.reported_at, ee.quotes, ee.added_at, v.retrieved_at, v.url
FROM news_event_evidence ee
JOIN news_article_versions v ON v.id = ee.version_id
WHERE ee.event_id = ANY(@event_ids::bigint[])
ORDER BY ee.event_id, ee.added_at, ee.version_id, ee.extraction_id, ee.relation;

-- name: ListNewsAdjustmentTeams :many
-- NHL team IDs by abbreviation, full and common name, for resolving a
-- reported trade destination.
SELECT DISTINCT ON (s.abbrev) s.abbrev, s.full_name, COALESCE(f.team_common_name, '')::text AS common_name, s.team_id
FROM season_teams s
LEFT JOIN franchises f ON f.id = s.franchise_id
WHERE s.team_kind = 'nhl'
ORDER BY s.abbrev, s.season DESC;
