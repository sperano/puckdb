-- Draft helper inputs: complete Yahoo stat category rules, versioned league
-- rule snapshots and the league's draftable player pool.
--
-- Yahoo league IDs are only unique within one Yahoo game (season), so the
-- legacy yahoo_* tables, keyed by the numeric league ID alone, cannot hold
-- the same ID for two seasons. The new tables are keyed by the full league
-- key ("<game_key>.l.<league_id>") and the season instead; the importer
-- refuses to overwrite a yahoo_leagues row that belongs to another season.

-- Stat category fields the scoring model needs. sort_order is Yahoo's value
-- verbatim (1 = higher is better, 0 = lower is better); NULL means Yahoo did
-- not say, which the draft helper reports instead of guessing. value (already
-- present) now holds the points weight from <stat_modifiers>.
ALTER TABLE yahoo_league_stat_categories
    ADD COLUMN display_name text DEFAULT ''::text NOT NULL,
    ADD COLUMN position_type text DEFAULT ''::text NOT NULL,
    ADD COLUMN sort_order smallint,
    ADD COLUMN is_only_display_stat boolean DEFAULT false NOT NULL;

-- One row per distinct version of a league's rules. Re-importing unchanged
-- rules only moves fetched_at/last_seen_at; any change inserts a new row.
CREATE TABLE yahoo_league_rule_snapshots (
    id bigserial PRIMARY KEY,
    season integer NOT NULL,
    league_id integer NOT NULL,
    league_key text NOT NULL,
    game_key integer,
    source text NOT NULL,
    source_season integer NOT NULL,
    source_league_key text NOT NULL,
    fetched_at timestamp with time zone NOT NULL,
    rules_hash text NOT NULL,
    rules jsonb NOT NULL,
    first_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT yahoo_league_rule_snapshots_source_check
        CHECK (source IN ('yahoo_api', 'temporary_stand_in')),
    CONSTRAINT yahoo_league_rule_snapshots_version_key
        UNIQUE (season, league_key, rules_hash)
);

CREATE INDEX idx_yahoo_league_rule_snapshots_league
    ON yahoo_league_rule_snapshots USING btree (season, league_id, last_seen_at DESC);

-- Every player Yahoo lists for the league (free agents, rostered players,
-- rookies without NHL history), with the league's current eligibility and
-- status. player_id is Yahoo's cross-season player ID; player_key carries the
-- game key of the season it was fetched for.
CREATE TABLE yahoo_league_players (
    league_key text NOT NULL,
    season integer NOT NULL,
    league_id integer NOT NULL,
    game_key integer NOT NULL,
    player_id integer NOT NULL,
    player_key text NOT NULL,
    full_name text NOT NULL,
    editorial_team_abbr text DEFAULT ''::text NOT NULL,
    display_position text DEFAULT ''::text NOT NULL,
    primary_position text DEFAULT ''::text NOT NULL,
    position_type text DEFAULT ''::text NOT NULL,
    eligible_positions text[] NOT NULL,
    status text DEFAULT ''::text NOT NULL,
    status_full text DEFAULT ''::text NOT NULL,
    injury_note text DEFAULT ''::text NOT NULL,
    on_disabled_list boolean DEFAULT false NOT NULL,
    fetched_at timestamp with time zone NOT NULL,
    imported_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (league_key, player_id)
);

CREATE INDEX idx_yahoo_league_players_season_league
    ON yahoo_league_players USING btree (season, league_id);
CREATE INDEX idx_yahoo_league_players_player
    ON yahoo_league_players USING btree (player_id);
