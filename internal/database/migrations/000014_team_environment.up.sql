-- nhl-baseline-v5 scales a skater's team-dependent rates when their
-- target-season club differs from the clubs behind their history (see
-- internal/projection/teamenv.go). Its two parameters join the typed config
-- columns; 0 (the default for earlier snapshots) means the adjustment was
-- off. team_environment records the per-player adjustment the ranking
-- explanations show; NULL when none applied.
ALTER TABLE projection_snapshots
    ADD COLUMN team_environment_prior_games double precision NOT NULL DEFAULT 0,
    ADD COLUMN team_environment_max_change double precision NOT NULL DEFAULT 0;

ALTER TABLE projection_players
    ADD COLUMN team_environment jsonb;

-- Speeds ListProjectionTeamSeasons' count of power-play opportunities
-- (minor and bench-minor penalties) per game and penalized club.
CREATE INDEX idx_play_events_minor_penalties
    ON play_events (game_id, event_owner_team_id)
    WHERE type_desc_key = 'penalty' AND penalty_type_code IN ('MIN', 'BEN');

-- nhl-baseline-v5 builds on v4 and fits its aging curve the same way.
ALTER TABLE projection_snapshots
    DROP CONSTRAINT projection_snapshots_v4_aging_curve_check,
    ADD CONSTRAINT projection_snapshots_aging_curve_check
        CHECK (model_version NOT IN ('nhl-baseline-v4', 'nhl-baseline-v5') OR aging_curve IS NOT NULL);
