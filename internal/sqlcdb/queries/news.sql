-- =============================================================================
-- Player news (draft helper): fetch coverage, articles, versions, mentions,
-- incident candidates and their evidence
-- =============================================================================

-- name: GetNewsFetchState :one
SELECT * FROM news_fetch_state
WHERE source_id = $1 AND scope = $2;

-- name: ListNewsFetchStates :many
SELECT * FROM news_fetch_state
ORDER BY source_id, scope;

-- name: RecordNewsFetchSuccess :exec
INSERT INTO news_fetch_state (
    source_id, scope, publisher, etag, last_modified, body_hash,
    last_attempt_at, last_success_at, data_as_of, last_items, last_new_versions
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, $9, $10)
ON CONFLICT (source_id, scope) DO UPDATE SET
    publisher = EXCLUDED.publisher,
    etag = EXCLUDED.etag,
    last_modified = EXCLUDED.last_modified,
    body_hash = EXCLUDED.body_hash,
    last_attempt_at = EXCLUDED.last_attempt_at,
    last_success_at = EXCLUDED.last_success_at,
    data_as_of = EXCLUDED.data_as_of,
    last_error = '',
    consecutive_failures = 0,
    last_items = EXCLUDED.last_items,
    last_new_versions = EXCLUDED.last_new_versions;

-- name: RecordNewsFetchFailure :exec
-- Leaves validators, last_success_at and data_as_of alone: the last good data
-- stays available and visibly ages.
INSERT INTO news_fetch_state (
    source_id, scope, publisher, last_attempt_at, last_failure_at, last_error, consecutive_failures
)
VALUES ($1, $2, $3, $4, $4, $5, 1)
ON CONFLICT (source_id, scope) DO UPDATE SET
    publisher = EXCLUDED.publisher,
    last_attempt_at = EXCLUDED.last_attempt_at,
    last_failure_at = EXCLUDED.last_failure_at,
    last_error = EXCLUDED.last_error,
    consecutive_failures = news_fetch_state.consecutive_failures + 1;

-- name: UpsertNewsArticle :one
INSERT INTO news_articles (publisher, external_id, source_id, kind, url, first_seen_at, last_seen_at, source_updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $6, $7)
ON CONFLICT (publisher, external_id) DO UPDATE SET
    url = EXCLUDED.url,
    last_seen_at = GREATEST(news_articles.last_seen_at, EXCLUDED.last_seen_at),
    source_updated_at = COALESCE(EXCLUDED.source_updated_at, news_articles.source_updated_at)
RETURNING id;

-- name: GetNewsArticleID :one
SELECT id FROM news_articles
WHERE publisher = $1 AND external_id = $2;

-- name: GetLatestNewsArticleVersion :one
SELECT id, version, content_hash FROM news_article_versions
WHERE article_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: InsertNewsArticleVersion :one
INSERT INTO news_article_versions (
    article_id, version, content_hash, title_fingerprint, text_fingerprint,
    title, evidence_text, body, author, url, published_at, source_updated_at,
    retrieved_at, subjects, team_hints, category_hint
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING id;

-- name: GetLatestNewsArticleBody :one
-- The body and subjects of an article's latest version, with the source
-- update time the article was last seen at: a story whose update time has
-- not moved reuses them instead of downloading its page again.
SELECT a.source_updated_at, v.body, v.subjects
FROM news_article_versions v
JOIN news_articles a ON a.id = v.article_id
WHERE a.publisher = $1 AND a.external_id = $2
ORDER BY v.version DESC
LIMIT 1;

-- name: ListUnprocessedNewsVersions :many
SELECT v.*, a.publisher, a.kind, a.source_id
FROM news_article_versions v
JOIN news_articles a ON a.id = v.article_id
WHERE v.processed_at IS NULL
ORDER BY v.id
LIMIT $1;

-- name: MarkNewsVersionProcessed :exec
UPDATE news_article_versions SET processed_at = $2
WHERE id = $1;

-- name: DeleteNewsMentions :exec
DELETE FROM news_mentions WHERE version_id = $1;

-- name: InsertNewsMention :exec
INSERT INTO news_mentions (
    version_id, ordinal, mention, role, resolution, method,
    nhl_player_id, yahoo_player_id, candidates
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListNewsIncidentCandidates :many
-- The player's incidents whose reported span overlaps [earliest, latest]
-- (a report's time widened by the clustering window). Every category is
-- returned: a reinstatement closes an earlier injury.
SELECT * FROM news_incidents
WHERE (nhl_player_id = sqlc.narg(nhl_player_id) OR yahoo_player_id = sqlc.narg(yahoo_player_id))
  AND first_reported_at <= @latest
  AND last_reported_at >= @earliest
ORDER BY last_reported_at DESC, id DESC;

-- name: ListNewsIncidentEvidenceKeys :many
SELECT e.incident_id, e.version_id, e.article_id, e.publisher,
       v.title_fingerprint, v.text_fingerprint
FROM news_incident_evidence e
JOIN news_article_versions v ON v.id = e.version_id
WHERE e.incident_id = ANY(@incident_ids::bigint[])
ORDER BY e.incident_id, e.reported_at, e.version_id;

-- name: CreateNewsIncident :one
INSERT INTO news_incidents (
    nhl_player_id, yahoo_player_id, player_name, category, first_reported_at, last_reported_at
)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING id;

-- name: ExtendNewsIncident :exec
-- Widens the reported span and fills in an identity learned later (an NHL ID
-- for a player first known only by Yahoo ID, or the reverse).
UPDATE news_incidents SET
    first_reported_at = LEAST(first_reported_at, @reported_at),
    last_reported_at = GREATEST(last_reported_at, @reported_at),
    nhl_player_id = COALESCE(nhl_player_id, sqlc.narg(nhl_player_id)),
    yahoo_player_id = COALESCE(yahoo_player_id, sqlc.narg(yahoo_player_id)),
    updated_at = NOW()
WHERE id = @id;

-- name: InsertNewsIncidentEvidence :exec
INSERT INTO news_incident_evidence (
    incident_id, version_id, article_id, publisher, kind, reported_at, relation, related_version_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (incident_id, version_id) DO NOTHING;

-- name: ListNewsIncidentsForPlayer :many
SELECT * FROM news_incidents
WHERE nhl_player_id = sqlc.narg(nhl_player_id) OR yahoo_player_id = sqlc.narg(yahoo_player_id)
ORDER BY first_reported_at, id;

-- name: ListRecentNewsIncidents :many
SELECT * FROM news_incidents
WHERE last_reported_at >= $1
ORDER BY last_reported_at DESC, id DESC
LIMIT $2;

-- name: ListNewsIncidentEvidence :many
SELECT e.incident_id, e.relation, e.reported_at, e.publisher, e.kind, e.related_version_id,
       v.id AS version_id, v.version, v.title, v.evidence_text, v.author, v.url,
       v.published_at, v.source_updated_at, v.retrieved_at, a.source_id,
       (SELECT MAX(lv.version) FROM news_article_versions lv WHERE lv.article_id = v.article_id)::integer
           AS latest_version
FROM news_incident_evidence e
JOIN news_article_versions v ON v.id = e.version_id
JOIN news_articles a ON a.id = v.article_id
WHERE e.incident_id = ANY(@incident_ids::bigint[])
ORDER BY e.incident_id, e.reported_at, v.id;

-- name: ListNewsMentionIssues :many
-- Subjects of the latest version of recent stories that did not resolve to
-- exactly one player.
SELECT m.mention, m.resolution, m.method, m.candidates,
       v.title, v.url, v.retrieved_at, a.publisher
FROM news_mentions m
JOIN news_article_versions v ON v.id = m.version_id
JOIN news_articles a ON a.id = v.article_id
WHERE m.role = 'subject'
  AND m.resolution <> 'resolved'
  AND v.retrieved_at >= $1
  AND v.version = (SELECT MAX(lv.version) FROM news_article_versions lv WHERE lv.article_id = v.article_id)
ORDER BY v.retrieved_at DESC, m.version_id, m.ordinal
LIMIT $2;

-- name: DeleteExpiredNewsIncidents :execrows
DELETE FROM news_incidents WHERE last_reported_at < $1;

-- name: DeleteExpiredNewsArticles :execrows
-- Articles not seen since the cutoff, unless they still back an incident or
-- an event.
DELETE FROM news_articles a
WHERE a.last_seen_at < $1
  AND NOT EXISTS (
      SELECT 1 FROM news_incident_evidence e
      JOIN news_article_versions v ON v.id = e.version_id
      WHERE v.article_id = a.id
  )
  AND NOT EXISTS (SELECT 1 FROM news_event_evidence ee WHERE ee.article_id = a.id);

-- name: DeleteExcessNewsArticleVersions :execrows
-- Keeps the newest @keep processed versions of each article, plus any version
-- that backs an incident or an event.
DELETE FROM news_article_versions v
WHERE v.processed_at IS NOT NULL
  AND v.version <= (SELECT MAX(lv.version) FROM news_article_versions lv WHERE lv.article_id = v.article_id) - @keep::integer
  AND NOT EXISTS (SELECT 1 FROM news_incident_evidence e WHERE e.version_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM news_event_evidence ee WHERE ee.version_id = v.id);

-- name: ListNewsResolverPlayers :many
-- NHL players with their most recent team abbreviation.
SELECT p.id, p.yahoo_id, p.first_name, p.last_name, p.is_active,
       COALESCE(st.abbrev, '')::text AS team_abbrev
FROM players p
LEFT JOIN LATERAL (
    SELECT s.abbrev FROM season_teams s
    WHERE s.team_id = p.team_id
    ORDER BY s.season DESC
    LIMIT 1
) st ON TRUE;

-- name: ListNewsYahooPlayers :many
-- One row per Yahoo player of the season's league pools (the newest fetch
-- when several leagues list the player), with the mapped NHL player.
SELECT DISTINCT ON (lp.player_id)
       lp.player_id, lp.full_name, lp.editorial_team_abbr, lp.status, lp.status_full,
       lp.injury_note, lp.on_disabled_list, lp.fetched_at, lp.league_key,
       p.id AS nhl_player_id
FROM yahoo_league_players lp
LEFT JOIN players p ON p.yahoo_id = lp.player_id
WHERE lp.season = $1
ORDER BY lp.player_id, lp.fetched_at DESC;

-- name: ListNewsYahooPoolFetches :many
SELECT league_key, MAX(fetched_at)::timestamptz AS fetched_at
FROM yahoo_league_players
WHERE season = $1
GROUP BY league_key
ORDER BY league_key;

-- name: ListNewsTeams :many
-- Current NHL team names for recognizing team mentions.
SELECT DISTINCT ON (s.abbrev) s.abbrev, s.full_name, COALESCE(f.team_common_name, '')::text AS common_name
FROM season_teams s
LEFT JOIN franchises f ON f.id = s.franchise_id
WHERE s.team_kind = 'nhl'
ORDER BY s.abbrev, s.season DESC;

-- name: LockNewsProcessing :exec
-- Serializes version processing across concurrent refreshes (a scheduled and
-- an on-demand run): released when the transaction ends.
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: GetNewsVersionProcessedAt :one
SELECT processed_at FROM news_article_versions
WHERE id = $1;
