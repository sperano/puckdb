-- Draft helper: validated news events extracted from stored article versions
-- by an LLM, their lifecycle, the evidence behind them and the extraction
-- audit trail.
--
-- The model only interprets supplied articles. Every event cites verbatim
-- quotes of an article version, names a player the news resolver supplied,
-- and never carries a value the quotes do not state (see
-- docs/draft-news-events.md). Nothing here ranks or penalizes a player.

-- One row per article version and extractor (provider, model, prompt and
-- schema version). Retries update the row; a new model, prompt or schema is
-- a new row, so the earlier outcome stays as the audit trail.
CREATE TABLE news_extractions (
    id bigserial PRIMARY KEY,
    version_id bigint NOT NULL REFERENCES news_article_versions(id) ON DELETE CASCADE,
    extractor_key text NOT NULL,
    provider text NOT NULL,
    model text NOT NULL,
    prompt_version text NOT NULL,
    schema_version text NOT NULL,
    -- Hash of the rendered model input: an identical input under the same
    -- extractor reuses a stored output instead of calling the model again.
    input_hash text NOT NULL,
    status text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    last_attempt_at timestamp with time zone,
    last_error text DEFAULT ''::text NOT NULL,
    -- The model's reply as received (bounded), validated again whenever it
    -- is reused.
    raw_output text DEFAULT ''::text NOT NULL,
    -- What validation dropped or cleared: unknown players, quotes not in the
    -- article, unsupported values, suspected instructions in the article.
    issues jsonb DEFAULT '[]'::jsonb NOT NULL,
    events integer DEFAULT 0 NOT NULL,
    prompt_tokens integer DEFAULT 0 NOT NULL,
    completion_tokens integer DEFAULT 0 NOT NULL,
    cached_from_id bigint REFERENCES news_extractions(id) ON DELETE SET NULL,
    reconciled_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT news_extractions_status_check CHECK (status IN ('pending', 'succeeded', 'invalid', 'failed')),
    CONSTRAINT news_extractions_version_extractor_key UNIQUE (version_id, extractor_key)
);

CREATE INDEX idx_news_extractions_cache
    ON news_extractions USING btree (extractor_key, input_hash) WHERE status = 'succeeded';

-- Validated events. An event's facts never change: a report with other
-- details (a length now stated, a rumor confirmed) adds a new event and
-- supersedes the old one, which keeps its own evidence.
CREATE TABLE news_events (
    id bigserial PRIMARY KEY,
    nhl_player_id bigint,
    yahoo_player_id integer,
    player_name text NOT NULL,
    event_type text NOT NULL,
    report_status text NOT NULL,
    attribution text DEFAULT ''::text NOT NULL,
    effective_from date,
    duration_kind text DEFAULT 'unknown'::text NOT NULL,
    duration_games integer,
    duration_days integer,
    duration_until date,
    change_field text DEFAULT ''::text NOT NULL,
    change_from text DEFAULT ''::text NOT NULL,
    change_to text DEFAULT ''::text NOT NULL,
    lifecycle text DEFAULT 'active'::text NOT NULL,
    superseded_by bigint REFERENCES news_events(id) ON DELETE SET NULL,
    lifecycle_reason text DEFAULT ''::text NOT NULL,
    lifecycle_changed_at timestamp with time zone,
    incident_id bigint REFERENCES news_incidents(id) ON DELETE SET NULL,
    -- Report times (publication, else source update, else retrieval) of the
    -- earliest and latest evidence supporting the event.
    first_reported_at timestamp with time zone NOT NULL,
    last_reported_at timestamp with time zone NOT NULL,
    needs_review boolean DEFAULT false NOT NULL,
    review_reason text DEFAULT ''::text NOT NULL,
    extraction_id bigint REFERENCES news_extractions(id) ON DELETE SET NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT news_events_type_check
        CHECK (event_type IN ('injury', 'suspension', 'reinstatement', 'trade', 'role_change')),
    CONSTRAINT news_events_report_status_check CHECK (report_status IN ('confirmed', 'reported', 'rumor')),
    CONSTRAINT news_events_duration_kind_check CHECK (duration_kind IN (
        'unknown', 'indefinite', 'games', 'days', 'until_date', 'day_to_day', 'week_to_week', 'month_to_month', 'season')),
    CONSTRAINT news_events_lifecycle_check CHECK (lifecycle IN ('active', 'superseded', 'retracted', 'resolved')),
    CONSTRAINT news_events_player_check CHECK (nhl_player_id IS NOT NULL OR yahoo_player_id IS NOT NULL)
);

CREATE INDEX idx_news_events_nhl_player ON news_events USING btree (nhl_player_id, event_type, last_reported_at DESC);
CREATE INDEX idx_news_events_yahoo_player ON news_events USING btree (yahoo_player_id, event_type, last_reported_at DESC);
CREATE INDEX idx_news_events_last_reported ON news_events USING btree (last_reported_at);

-- The article versions behind an event, per extraction, with the verbatim
-- quotes the model cited. relation: supports (reports the event),
-- contradicts (denies it without the authority to retract it), retracts
-- (a correction or authoritative denial), resolves (a reinstatement),
-- withdraws (a newer version of a supporting article no longer reports it).
CREATE TABLE news_event_evidence (
    event_id bigint NOT NULL REFERENCES news_events(id) ON DELETE CASCADE,
    version_id bigint NOT NULL REFERENCES news_article_versions(id) ON DELETE CASCADE,
    extraction_id bigint NOT NULL REFERENCES news_extractions(id) ON DELETE CASCADE,
    relation text NOT NULL,
    article_id bigint NOT NULL,
    publisher text NOT NULL,
    kind text NOT NULL,
    reported_at timestamp with time zone NOT NULL,
    quotes jsonb DEFAULT '[]'::jsonb NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (event_id, version_id, extraction_id, relation),
    CONSTRAINT news_event_evidence_relation_check
        CHECK (relation IN ('supports', 'contradicts', 'retracts', 'resolves', 'withdraws'))
);

CREATE INDEX idx_news_event_evidence_version ON news_event_evidence USING btree (version_id);

-- Every lifecycle change of an event, with the report and extraction that
-- caused it.
CREATE TABLE news_event_transitions (
    id bigserial PRIMARY KEY,
    event_id bigint NOT NULL REFERENCES news_events(id) ON DELETE CASCADE,
    from_lifecycle text NOT NULL,
    to_lifecycle text NOT NULL,
    version_id bigint REFERENCES news_article_versions(id) ON DELETE SET NULL,
    extraction_id bigint REFERENCES news_extractions(id) ON DELETE SET NULL,
    reason text NOT NULL,
    at timestamp with time zone NOT NULL
);

CREATE INDEX idx_news_event_transitions_event ON news_event_transitions USING btree (event_id, at);

-- Runs of the labeled evaluation corpus. Automatic numeric effects stay off
-- for an extractor until its latest run on the current corpus passed.
CREATE TABLE news_extraction_evaluations (
    id bigserial PRIMARY KEY,
    extractor_key text NOT NULL,
    corpus_version text NOT NULL,
    cases integer NOT NULL,
    passed boolean NOT NULL,
    metrics jsonb NOT NULL,
    run_at timestamp with time zone NOT NULL
);

CREATE INDEX idx_news_extraction_evaluations_key
    ON news_extraction_evaluations USING btree (extractor_key, corpus_version, run_at DESC);
