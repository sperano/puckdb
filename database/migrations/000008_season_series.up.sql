-- Season series data: game officials, head coaches, and scratched players.
-- This data is only available via the NHL API's "right-rail" (season series) endpoint
-- and is not present in boxscores, play-by-play, or game story responses.

-- Game officials (referees and linesmen)
CREATE TABLE IF NOT EXISTS game_officials (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('referee', 'linesman')),
    sequence SMALLINT NOT NULL,
    name TEXT NOT NULL,
    PRIMARY KEY (game_id, role, sequence)
);

-- Head coaches per team per game
CREATE TABLE IF NOT EXISTS game_coaches (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    team_id BIGINT NOT NULL,
    head_coach TEXT NOT NULL,
    PRIMARY KEY (game_id, team_id)
);

-- Scratched players (on roster but not dressing for the game)
CREATE TABLE IF NOT EXISTS game_scratches (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    team_id BIGINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    PRIMARY KEY (game_id, player_id)
);

CREATE INDEX IF NOT EXISTS idx_game_scratches_player ON game_scratches(player_id);
