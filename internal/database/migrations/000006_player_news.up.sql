-- Draft helper player news: attributable articles and structured status
-- updates, their versions, the players they name, and the incident
-- candidates repeated or syndicated reports are grouped into.
--
-- Nothing here scores a player. An incident is one candidate event (an
-- injury, a suspension, ...) for one player; however many stories report it,
-- it stays one row, and its evidence rows say which reports are independent.

-- Fetch coverage per source and scope ('feed' for a whole feed, or e.g.
-- 'season:2026' for the Yahoo status of a season's pools). A failed fetch only
-- touches the attempt/failure columns, so the last good data stays in place
-- and last_success_at shows how old it is.
CREATE TABLE news_fetch_state (
    source_id text NOT NULL,
    scope text NOT NULL,
    publisher text NOT NULL,
    etag text DEFAULT ''::text NOT NULL,
    last_modified text DEFAULT ''::text NOT NULL,
    body_hash text DEFAULT ''::text NOT NULL,
    last_attempt_at timestamp with time zone,
    last_success_at timestamp with time zone,
    -- data_as_of is when the underlying data was current, when that differs
    -- from the fetch (Yahoo status is as old as the imported player pools).
    data_as_of timestamp with time zone,
    last_failure_at timestamp with time zone,
    last_error text DEFAULT ''::text NOT NULL,
    consecutive_failures integer DEFAULT 0 NOT NULL,
    last_items integer DEFAULT 0 NOT NULL,
    last_new_versions integer DEFAULT 0 NOT NULL,
    PRIMARY KEY (source_id, scope)
);

-- One row per story, identified by its publisher's own ID (the same NHL.com
-- story reached through two tag feeds is one article).
CREATE TABLE news_articles (
    id bigserial PRIMARY KEY,
    publisher text NOT NULL,
    external_id text NOT NULL,
    source_id text NOT NULL,
    kind text NOT NULL,
    url text NOT NULL,
    first_seen_at timestamp with time zone NOT NULL,
    last_seen_at timestamp with time zone NOT NULL,
    CONSTRAINT news_articles_kind_check CHECK (kind IN ('official', 'structured', 'reporting')),
    CONSTRAINT news_articles_identity_key UNIQUE (publisher, external_id)
);

CREATE INDEX idx_news_articles_last_seen ON news_articles USING btree (last_seen_at);

-- Every distinct content of an article. A new row is added only when the
-- content hash differs from the latest version, so an unchanged article is
-- never reprocessed and an update or correction always is.
-- evidence_text is only what the publisher syndicates in its feed (summary or
-- description), trimmed to a bounded length; the full story text, when a
-- source offers it, is the body column added by 000007.
CREATE TABLE news_article_versions (
    id bigserial PRIMARY KEY,
    article_id bigint NOT NULL REFERENCES news_articles(id) ON DELETE CASCADE,
    version integer NOT NULL,
    content_hash text NOT NULL,
    title_fingerprint text NOT NULL,
    text_fingerprint text NOT NULL,
    title text NOT NULL,
    evidence_text text NOT NULL,
    author text DEFAULT ''::text NOT NULL,
    url text NOT NULL,
    published_at timestamp with time zone,
    source_updated_at timestamp with time zone,
    retrieved_at timestamp with time zone NOT NULL,
    -- Players the source itself tags (NHL player IDs, Yahoo player IDs).
    subjects jsonb DEFAULT '[]'::jsonb NOT NULL,
    -- NHL team abbreviations the source attaches to the story.
    team_hints text[] DEFAULT '{}'::text[] NOT NULL,
    -- A category the source states in structured form (Yahoo status codes).
    category_hint text DEFAULT ''::text NOT NULL,
    processed_at timestamp with time zone,
    CONSTRAINT news_article_versions_version_key UNIQUE (article_id, version)
);

CREATE INDEX idx_news_article_versions_unprocessed
    ON news_article_versions USING btree (id) WHERE processed_at IS NULL;

-- Player names or IDs found in a version, with how they resolved. Ambiguous
-- and unresolved mentions are kept for review and never attached to a player.
CREATE TABLE news_mentions (
    version_id bigint NOT NULL REFERENCES news_article_versions(id) ON DELETE CASCADE,
    ordinal integer NOT NULL,
    mention text NOT NULL,
    role text NOT NULL,
    resolution text NOT NULL,
    method text NOT NULL,
    nhl_player_id bigint,
    yahoo_player_id integer,
    candidates jsonb DEFAULT '[]'::jsonb NOT NULL,
    PRIMARY KEY (version_id, ordinal),
    CONSTRAINT news_mentions_role_check CHECK (role IN ('subject', 'mentioned')),
    CONSTRAINT news_mentions_resolution_check CHECK (resolution IN ('resolved', 'ambiguous', 'unresolved'))
);

CREATE INDEX idx_news_mentions_nhl_player ON news_mentions USING btree (nhl_player_id);
CREATE INDEX idx_news_mentions_yahoo_player ON news_mentions USING btree (yahoo_player_id);

-- Incident candidates: one per player, category and time window, however
-- many reports repeat it. Reported times come from the evidence (publication
-- time, else source update time, else retrieval time).
CREATE TABLE news_incidents (
    id bigserial PRIMARY KEY,
    nhl_player_id bigint,
    yahoo_player_id integer,
    player_name text NOT NULL,
    category text NOT NULL,
    first_reported_at timestamp with time zone NOT NULL,
    last_reported_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT news_incidents_category_check
        CHECK (category IN ('injury', 'suspension', 'reinstatement', 'trade', 'role_change')),
    CONSTRAINT news_incidents_player_check CHECK (nhl_player_id IS NOT NULL OR yahoo_player_id IS NOT NULL)
);

CREATE INDEX idx_news_incidents_nhl_player
    ON news_incidents USING btree (nhl_player_id, category, last_reported_at DESC);
CREATE INDEX idx_news_incidents_yahoo_player
    ON news_incidents USING btree (yahoo_player_id, category, last_reported_at DESC);
CREATE INDEX idx_news_incidents_last_reported ON news_incidents USING btree (last_reported_at);

-- The reports behind an incident. relation says whether a report is an
-- independent source or repeats one already attached: a syndicated copy, a
-- follow-up from the same publisher, or a revision of the same article.
CREATE TABLE news_incident_evidence (
    incident_id bigint NOT NULL REFERENCES news_incidents(id) ON DELETE CASCADE,
    version_id bigint NOT NULL REFERENCES news_article_versions(id) ON DELETE CASCADE,
    article_id bigint NOT NULL,
    publisher text NOT NULL,
    kind text NOT NULL,
    reported_at timestamp with time zone NOT NULL,
    relation text NOT NULL,
    related_version_id bigint,
    added_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (incident_id, version_id),
    CONSTRAINT news_incident_evidence_relation_check
        CHECK (relation IN ('independent', 'syndicated', 'same_publisher', 'revision'))
);

CREATE INDEX idx_news_incident_evidence_version ON news_incident_evidence USING btree (version_id);
