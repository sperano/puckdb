-- =============================================================================
-- Player news events (draft helper): LLM extractions of article versions,
-- validated events, their evidence and lifecycle, and evaluation runs
-- =============================================================================

-- name: ListNewsVersionsToExtract :many
-- Latest versions of reporting and official articles that back a recent
-- incident, or whose earlier version backs an event (so a correction is
-- read), and that the extractor has not settled: never tried, or a pending
-- or failed attempt, or a successful output not yet reconciled, with
-- attempts left and past the retry delay. Oldest report first.
SELECT v.id, v.article_id, v.version, v.title, v.evidence_text, v.body, v.author, v.url,
       v.published_at, v.source_updated_at, v.retrieved_at,
       a.publisher, a.kind, a.source_id
FROM news_article_versions v
JOIN news_articles a ON a.id = v.article_id
WHERE v.processed_at IS NOT NULL
  AND a.kind <> 'structured'
  AND v.version = (SELECT MAX(lv.version) FROM news_article_versions lv WHERE lv.article_id = v.article_id)
  AND (
      EXISTS (
          SELECT 1 FROM news_incident_evidence e
          JOIN news_incidents i ON i.id = e.incident_id
          WHERE e.version_id = v.id AND i.last_reported_at >= @since
      )
      OR EXISTS (
          SELECT 1 FROM news_event_evidence ee
          JOIN news_article_versions ov ON ov.id = ee.version_id
          WHERE ov.article_id = v.article_id AND ov.version < v.version
      )
  )
  AND NOT EXISTS (
      SELECT 1 FROM news_extractions x
      WHERE x.version_id = v.id AND x.extractor_key = @extractor_key
        AND (
            (x.status = 'succeeded' AND x.reconciled_at IS NOT NULL)
            OR x.status = 'invalid'
            OR x.attempts >= @max_attempts::integer
            OR x.last_attempt_at > @retry_before
        )
  )
ORDER BY COALESCE(v.published_at, v.source_updated_at, v.retrieved_at), v.id
LIMIT @max_versions;

-- name: ListNewsVersionPlayers :many
-- Players a version names that resolved to one identity, subjects first,
-- named as the NHL directory spells them (a matched mention is stored
-- normalized).
SELECT m.nhl_player_id, m.yahoo_player_id, m.role,
       COALESCE(NULLIF(TRIM(p.first_name || ' ' || p.last_name), ''), m.mention)::text AS name
FROM news_mentions m
LEFT JOIN players p ON p.id = m.nhl_player_id
WHERE m.version_id = $1 AND m.resolution = 'resolved'
ORDER BY CASE m.role WHEN 'subject' THEN 0 ELSE 1 END, m.ordinal;

-- name: ListNewsArticleEventPlayers :many
-- Players of events an earlier version of the article supports: a
-- correction has to be able to name them.
SELECT DISTINCT ev.nhl_player_id, ev.yahoo_player_id, ev.player_name
FROM news_events ev
JOIN news_event_evidence ee ON ee.event_id = ev.id AND ee.relation = 'supports'
JOIN news_article_versions ov ON ov.id = ee.version_id
WHERE ov.article_id = @article_id AND ov.version < @version::integer;

-- name: GetNewsExtraction :one
SELECT * FROM news_extractions
WHERE version_id = $1 AND extractor_key = $2;

-- name: StartNewsExtraction :one
-- Records an attempt before the model is called, so a crash still counts
-- against the attempt limit. An attempt another refresh started after
-- @stale_before is left alone and no row is returned: that refresh is
-- extracting the version.
INSERT INTO news_extractions (
    version_id, extractor_key, provider, model, prompt_version, schema_version,
    input_hash, status, attempts, last_attempt_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', 1, $8)
ON CONFLICT (version_id, extractor_key) DO UPDATE SET
    input_hash = EXCLUDED.input_hash,
    status = 'pending',
    attempts = news_extractions.attempts + 1,
    last_attempt_at = EXCLUDED.last_attempt_at,
    updated_at = NOW()
WHERE news_extractions.status <> 'pending' OR news_extractions.last_attempt_at <= @stale_before
RETURNING id, attempts;

-- name: FindCachedNewsExtraction :one
-- A successful output of the same extractor for an identical input.
SELECT id, raw_output FROM news_extractions
WHERE extractor_key = $1 AND input_hash = $2 AND status = 'succeeded'
ORDER BY id
LIMIT 1;

-- name: FinishNewsExtraction :exec
UPDATE news_extractions SET
    status = $2,
    last_error = $3,
    raw_output = $4,
    issues = $5,
    events = $6,
    prompt_tokens = prompt_tokens + @add_prompt_tokens::integer,
    completion_tokens = completion_tokens + @add_completion_tokens::integer,
    cached_from_id = sqlc.narg(cached_from_id),
    updated_at = NOW()
WHERE id = $1;

-- name: MarkNewsExtractionReconciled :exec
UPDATE news_extractions SET reconciled_at = $2, last_error = '', updated_at = NOW()
WHERE id = $1;

-- name: NoteNewsExtractionReconcileFailure :exec
-- A successful output whose events could not be reconciled: the failure
-- counts as an attempt, so the version waits for the retry delay and is
-- listed for review once out of attempts instead of blocking the queue.
UPDATE news_extractions SET
    attempts = attempts + 1,
    last_attempt_at = $2,
    last_error = $3,
    updated_at = NOW()
WHERE id = $1;

-- name: IsNewsExtractionReconciled :one
SELECT (reconciled_at IS NOT NULL)::boolean AS reconciled FROM news_extractions
WHERE id = $1;

-- name: ListNewsEventsNear :many
-- The players' active events and their other events reported since @since,
-- plus every event an earlier version of the article supports, whatever its
-- age or lifecycle.
SELECT * FROM news_events ev
WHERE ((ev.nhl_player_id = ANY(@nhl_player_ids::bigint[]) OR ev.yahoo_player_id = ANY(@yahoo_player_ids::integer[]))
       AND (ev.lifecycle = 'active' OR ev.last_reported_at >= @since))
   OR EXISTS (
       SELECT 1 FROM news_event_evidence ee
       JOIN news_article_versions ov ON ov.id = ee.version_id
       WHERE ee.event_id = ev.id AND ee.relation = 'supports' AND ov.article_id = @article_id
   )
ORDER BY ev.first_reported_at, ev.id;

-- name: ListNewsEventSupport :many
-- The article versions supporting each event.
SELECT DISTINCT ee.event_id, ee.article_id, ee.version_id, ee.publisher, ee.kind
FROM news_event_evidence ee
WHERE ee.event_id = ANY(@event_ids::bigint[]) AND ee.relation = 'supports'
ORDER BY ee.event_id, ee.article_id, ee.version_id;

-- name: CreateNewsEvent :one
INSERT INTO news_events (
    nhl_player_id, yahoo_player_id, player_name, event_type, report_status, attribution,
    effective_from, duration_kind, duration_games, duration_days, duration_until,
    change_field, change_from, change_to, lifecycle, superseded_by, lifecycle_reason, lifecycle_changed_at,
    incident_id, first_reported_at, last_reported_at, needs_review, review_reason, extraction_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $20, $21, $22, $23)
RETURNING id;

-- name: TouchNewsEvent :exec
-- Widens the event's reported span to a report that supports it.
UPDATE news_events SET
    first_reported_at = LEAST(first_reported_at, @reported_at),
    last_reported_at = GREATEST(last_reported_at, @reported_at),
    updated_at = NOW()
WHERE id = @id;

-- name: SetNewsEventLifecycle :exec
UPDATE news_events SET
    lifecycle = $2,
    lifecycle_reason = $3,
    lifecycle_changed_at = $4,
    superseded_by = sqlc.narg(superseded_by),
    updated_at = NOW()
WHERE id = $1;

-- name: FlagNewsEventReview :exec
-- Adds a review reason once, however often the same report is reconciled.
UPDATE news_events SET
    needs_review = TRUE,
    review_reason = CASE
        WHEN review_reason = '' THEN @reason::text
        WHEN position(@reason::text IN review_reason) > 0 THEN review_reason
        ELSE review_reason || '; ' || @reason::text
    END,
    updated_at = NOW()
WHERE id = @id;

-- name: InsertNewsEventEvidence :execrows
INSERT INTO news_event_evidence (
    event_id, version_id, extraction_id, relation, article_id, publisher, kind, reported_at, quotes
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (event_id, version_id, extraction_id, relation) DO NOTHING;

-- name: InsertNewsEventTransition :exec
INSERT INTO news_event_transitions (event_id, from_lifecycle, to_lifecycle, version_id, extraction_id, reason, at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetNewsVersionIncident :one
-- The incident a version backs for a player and category, if any.
SELECT i.id FROM news_incidents i
JOIN news_incident_evidence e ON e.incident_id = i.id
WHERE e.version_id = @version_id AND i.category = @category
  AND (i.nhl_player_id = sqlc.narg(nhl_player_id) OR i.yahoo_player_id = sqlc.narg(yahoo_player_id))
ORDER BY i.id
LIMIT 1;

-- name: ListRecentNewsEvents :many
SELECT * FROM news_events
WHERE last_reported_at >= $1
ORDER BY last_reported_at DESC, id DESC
LIMIT $2;

-- name: ListNewsEventsForPlayer :many
SELECT * FROM news_events
WHERE nhl_player_id = sqlc.narg(nhl_player_id) OR yahoo_player_id = sqlc.narg(yahoo_player_id)
ORDER BY first_reported_at, id;

-- name: ListNewsEventEvidence :many
SELECT ee.event_id, ee.relation, ee.reported_at, ee.publisher, ee.kind, ee.quotes, ee.extraction_id,
       v.id AS version_id, v.version, v.title, v.url
FROM news_event_evidence ee
JOIN news_article_versions v ON v.id = ee.version_id
WHERE ee.event_id = ANY(@event_ids::bigint[])
ORDER BY ee.event_id, ee.reported_at, v.id, ee.relation;

-- name: ListNewsEventTransitions :many
SELECT * FROM news_event_transitions
WHERE event_id = ANY(@event_ids::bigint[])
ORDER BY event_id, at, id;

-- name: ListNewsExtractionReviews :many
-- Extractions that need a person: invalid output, failures out of attempts
-- (calls or reconciling), or output validation had to drop or clear. Only the latest version of
-- each article is listed.
SELECT x.id, x.version_id, x.extractor_key, x.status, x.attempts, x.last_attempt_at, x.last_error,
       x.issues, v.title, v.url, a.publisher
FROM news_extractions x
JOIN news_article_versions v ON v.id = x.version_id
JOIN news_articles a ON a.id = v.article_id
WHERE x.last_attempt_at >= @since
  AND v.version = (SELECT MAX(lv.version) FROM news_article_versions lv WHERE lv.article_id = v.article_id)
  AND (x.status = 'invalid'
       OR (x.status IN ('pending', 'failed') AND x.attempts >= @max_attempts::integer)
       OR (x.status = 'succeeded' AND x.reconciled_at IS NULL AND x.attempts >= @max_attempts::integer)
       OR jsonb_array_length(x.issues) > 0)
ORDER BY x.last_attempt_at DESC, x.id DESC
LIMIT @max_rows;

-- name: InsertNewsExtractionEvaluation :one
INSERT INTO news_extraction_evaluations (extractor_key, corpus_version, cases, passed, metrics, run_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id;

-- name: GetLatestNewsExtractionEvaluation :one
SELECT * FROM news_extraction_evaluations
WHERE extractor_key = $1 AND corpus_version = $2
ORDER BY run_at DESC, id DESC
LIMIT 1;

-- name: DeleteExpiredNewsEvents :execrows
DELETE FROM news_events WHERE last_reported_at < $1;
