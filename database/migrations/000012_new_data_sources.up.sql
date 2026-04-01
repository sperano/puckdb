-- =============================================================================
-- New Data Sources: NHL API + Yahoo Fantasy enrichment
-- =============================================================================

-- Daily standings snapshots from NHL API LeagueStandingsForDate
CREATE TABLE IF NOT EXISTS standings_snapshots (
    season INT NOT NULL,
    date DATE NOT NULL,
    team_abbrev TEXT NOT NULL,
    wins INT NOT NULL,
    losses INT NOT NULL,
    ot_losses INT NOT NULL,
    points INT NOT NULL,
    division_abbrev TEXT NOT NULL,
    division_name TEXT NOT NULL,
    conference_abbrev TEXT,
    conference_name TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (season, date, team_abbrev)
);

CREATE INDEX IF NOT EXISTS idx_standings_snapshots_date ON standings_snapshots(date);

-- Full roster per team per season from NHL API RosterSeason
CREATE TABLE IF NOT EXISTS season_rosters (
    season INT NOT NULL REFERENCES seasons(id),
    team_id BIGINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    position TEXT NOT NULL,
    shoots_catches TEXT NOT NULL,
    sweater_number SMALLINT NOT NULL,
    height_inches SMALLINT NOT NULL,
    weight_pounds SMALLINT NOT NULL,
    birth_date TEXT NOT NULL,
    birth_city TEXT,
    birth_state_province TEXT,
    birth_country TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (season, team_id, player_id)
);

CREATE INDEX IF NOT EXISTS idx_season_rosters_player ON season_rosters(player_id);

-- Team-level per-player skater season aggregates from NHL API ClubStats
CREATE TABLE IF NOT EXISTS club_skater_stats (
    season INT NOT NULL,
    game_type SMALLINT NOT NULL,
    team_id BIGINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    games_played INT NOT NULL,
    goals INT NOT NULL,
    assists INT NOT NULL,
    points INT NOT NULL,
    plus_minus INT NOT NULL,
    penalty_minutes INT NOT NULL,
    power_play_goals INT NOT NULL,
    shorthanded_goals INT NOT NULL,
    game_winning_goals INT NOT NULL,
    overtime_goals INT NOT NULL,
    shots INT NOT NULL,
    shooting_pctg REAL NOT NULL,
    avg_toi_per_game REAL NOT NULL,
    avg_shifts_per_game REAL NOT NULL,
    faceoff_win_pctg REAL NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (season, game_type, team_id, player_id)
);

-- Team-level per-goalie season aggregates from NHL API ClubStats
CREATE TABLE IF NOT EXISTS club_goalie_stats (
    season INT NOT NULL,
    game_type SMALLINT NOT NULL,
    team_id BIGINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    games_played INT NOT NULL,
    games_started INT NOT NULL,
    wins INT NOT NULL,
    losses INT NOT NULL,
    overtime_losses INT NOT NULL,
    goals_against_average REAL NOT NULL,
    save_percentage REAL NOT NULL,
    shots_against INT NOT NULL,
    saves INT NOT NULL,
    goals_against INT NOT NULL,
    shutouts INT NOT NULL,
    goals INT NOT NULL,
    assists INT NOT NULL,
    points INT NOT NULL,
    penalty_minutes INT NOT NULL,
    toi_seconds BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (season, game_type, team_id, player_id)
);

-- Player awards from cached PlayerLanding files
CREATE TABLE IF NOT EXISTS player_awards (
    player_id BIGINT NOT NULL REFERENCES players(id),
    trophy_name TEXT NOT NULL,
    season INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (player_id, trophy_name, season)
);

CREATE INDEX IF NOT EXISTS idx_player_awards_trophy ON player_awards(trophy_name);
CREATE INDEX IF NOT EXISTS idx_player_awards_season ON player_awards(season);

-- Career season-by-season stats from cached PlayerLanding (all leagues)
CREATE TABLE IF NOT EXISTS player_season_totals (
    player_id BIGINT NOT NULL REFERENCES players(id),
    season INT NOT NULL,
    game_type SMALLINT NOT NULL,
    league_abbrev TEXT NOT NULL,
    team_name TEXT NOT NULL,
    sequence INT NOT NULL DEFAULT 0,
    games_played INT NOT NULL,
    goals INT,
    assists INT,
    points INT,
    plus_minus INT,
    pim INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (player_id, season, game_type, league_abbrev, sequence)
);

CREATE INDEX IF NOT EXISTS idx_player_season_totals_league ON player_season_totals(league_abbrev);

-- TV broadcast info per game from cached Boxscore.TVBroadcasts
CREATE TABLE IF NOT EXISTS game_broadcasts (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    broadcast_id BIGINT NOT NULL,
    market TEXT NOT NULL,
    country_code TEXT NOT NULL,
    network TEXT NOT NULL,
    sequence_number INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (game_id, broadcast_id)
);

-- Yahoo Fantasy transactions
CREATE TABLE IF NOT EXISTS yahoo_transactions (
    league_id INT NOT NULL,
    transaction_key TEXT NOT NULL,
    type TEXT NOT NULL,
    timestamp BIGINT,
    status TEXT,
    players JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (league_id, transaction_key)
);

CREATE INDEX IF NOT EXISTS idx_yahoo_transactions_type ON yahoo_transactions(league_id, type);

-- Yahoo Fantasy draft results
CREATE TABLE IF NOT EXISTS yahoo_draft_results (
    league_id INT NOT NULL,
    round INT NOT NULL,
    pick INT NOT NULL,
    team_id INT NOT NULL,
    player_id INT NOT NULL,
    cost INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (league_id, round, pick)
);

-- Yahoo Fantasy matchups (weekly head-to-head)
CREATE TABLE IF NOT EXISTS yahoo_matchups (
    league_id INT NOT NULL,
    week INT NOT NULL,
    team1_id INT NOT NULL,
    team2_id INT NOT NULL,
    team1_points REAL,
    team2_points REAL,
    status TEXT,
    is_playoffs BOOLEAN NOT NULL DEFAULT FALSE,
    is_consolation BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (league_id, week, team1_id, team2_id)
);

-- =============================================================================
-- Column additions to existing tables
-- =============================================================================

-- Goalie shots-against breakdown (compound strings like "12 of 14")
ALTER TABLE game_goalie_stats
    ADD COLUMN IF NOT EXISTS even_strength_shots_against TEXT,
    ADD COLUMN IF NOT EXISTS power_play_shots_against TEXT,
    ADD COLUMN IF NOT EXISTS shorthanded_shots_against TEXT;

-- Yahoo roster enrichment: player details currently parsed but dropped
ALTER TABLE yahoo_team_rosters
    ADD COLUMN IF NOT EXISTS player_status TEXT,
    ADD COLUMN IF NOT EXISTS player_status_full TEXT,
    ADD COLUMN IF NOT EXISTS injury_note TEXT,
    ADD COLUMN IF NOT EXISTS on_disabled_list BOOLEAN,
    ADD COLUMN IF NOT EXISTS position_type TEXT,
    ADD COLUMN IF NOT EXISTS display_position TEXT,
    ADD COLUMN IF NOT EXISTS primary_position TEXT,
    ADD COLUMN IF NOT EXISTS eligible_positions TEXT[],
    ADD COLUMN IF NOT EXISTS uniform_number INT,
    ADD COLUMN IF NOT EXISTS editorial_team_abbr TEXT;
