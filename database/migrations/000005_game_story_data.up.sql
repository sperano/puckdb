-- Game Story data: three stars, goal highlights, shootout attempts
-- This data is unique to GameStory API responses and not available in boxscores.
--
-- NOTE: penalties_drawn is not implemented. The NHL API's PenaltySummary.DrawnBy
-- only contains firstName, lastName, and sweaterNumber - no player ID. Matching
-- players would require fuzzy name matching within the game's roster, which is
-- error-prone. Could be added later if the API adds player IDs to penalty data.

-- Three Stars of the game (post-game star selections)
CREATE TABLE IF NOT EXISTS game_three_stars (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    star SMALLINT NOT NULL CHECK (star BETWEEN 1 AND 3),
    player_id BIGINT NOT NULL REFERENCES players(id),
    PRIMARY KEY (game_id, star)
);

CREATE INDEX IF NOT EXISTS idx_game_three_stars_player ON game_three_stars(player_id);

-- Goal highlights (video clip URLs for each goal)
CREATE TABLE IF NOT EXISTS goal_highlights (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    event_id BIGINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    period SMALLINT NOT NULL,
    time_in_period TEXT NOT NULL,
    goals_to_date SMALLINT,
    highlight_clip_id BIGINT,
    highlight_clip_url TEXT,
    discrete_clip_id BIGINT,
    PRIMARY KEY (game_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_goal_highlights_player ON goal_highlights(player_id);

-- Shootout attempts (individual shootout shot results)
CREATE TABLE IF NOT EXISTS shootout_attempts (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    sequence SMALLINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    team_id BIGINT NOT NULL,
    shot_type TEXT NOT NULL,
    result TEXT NOT NULL,  -- 'goal', 'save', 'miss'
    game_winner BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (game_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_shootout_attempts_player ON shootout_attempts(player_id);
