-- Extended data sources: NHL API enrichment + Yahoo Fantasy normalization

-- Daily standings snapshots (team_id as PK, not team_abbrev)
CREATE TABLE standings_snapshots (
    season INT NOT NULL REFERENCES seasons(id),
    date DATE NOT NULL,
    team_id BIGINT NOT NULL,
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
    PRIMARY KEY (season, date, team_id),
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id)
);

CREATE INDEX idx_standings_snapshots_date ON standings_snapshots(date);
CREATE INDEX idx_standings_snapshots_team_id ON standings_snapshots(team_id);

-- Full roster per team per season
CREATE TABLE season_rosters (
    season INT NOT NULL REFERENCES seasons(id),
    team_id BIGINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    position player_position,
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
    PRIMARY KEY (season, team_id, player_id),
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id)
);

CREATE INDEX idx_season_rosters_player ON season_rosters(player_id);

-- Team-level per-player skater season aggregates
CREATE TABLE club_skater_stats (
    season INT NOT NULL,
    game_type game_type NOT NULL,
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
    PRIMARY KEY (season, game_type, team_id, player_id),
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id)
);

-- Team-level per-goalie season aggregates
CREATE TABLE club_goalie_stats (
    season INT NOT NULL,
    game_type game_type NOT NULL,
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
    PRIMARY KEY (season, game_type, team_id, player_id),
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id)
);

-- Player awards from cached PlayerLanding
CREATE TABLE player_awards (
    player_id BIGINT NOT NULL REFERENCES players(id),
    trophy_name TEXT NOT NULL,
    season INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (player_id, trophy_name, season)
);

CREATE INDEX idx_player_awards_trophy ON player_awards(trophy_name);
CREATE INDEX idx_player_awards_season ON player_awards(season);

-- Career season-by-season stats (all leagues, concatenated season format)
CREATE TABLE player_season_totals (
    player_id BIGINT NOT NULL REFERENCES players(id),
    season INT NOT NULL,
    game_type game_type NOT NULL,
    league_abbrev TEXT NOT NULL,
    team_name TEXT NOT NULL,
    team_id BIGINT,
    sequence INT NOT NULL DEFAULT 0,
    games_played INT NOT NULL,
    goals INT,
    assists INT,
    points INT,
    plus_minus INT,
    pim INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (player_id, season, game_type, league_abbrev, sequence),
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id)
        ON DELETE SET NULL (team_id)
);

CREATE INDEX idx_player_season_totals_league ON player_season_totals(league_abbrev);
CREATE INDEX idx_player_season_totals_team_id ON player_season_totals(team_id) WHERE team_id IS NOT NULL;

-- TV broadcast info per game
CREATE TABLE game_broadcasts (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    broadcast_id BIGINT NOT NULL,
    market TEXT NOT NULL,
    country_code TEXT NOT NULL,
    network TEXT NOT NULL,
    sequence_number INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (game_id, broadcast_id)
);

-- Yahoo Fantasy transactions (no JSONB players column — normalized into yahoo_transaction_players)
CREATE TABLE yahoo_transactions (
    league_id INT NOT NULL REFERENCES yahoo_leagues(id),
    transaction_key TEXT NOT NULL,
    type TEXT NOT NULL,
    timestamp BIGINT,
    status TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (league_id, transaction_key)
);

CREATE INDEX idx_yahoo_transactions_type ON yahoo_transactions(league_id, type);

-- Normalized transaction players
CREATE TABLE yahoo_transaction_players (
    league_id INT NOT NULL,
    transaction_key TEXT NOT NULL,
    player_id INT NOT NULL,
    player_key TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    source_type TEXT NOT NULL DEFAULT '',
    source_team_key TEXT NOT NULL DEFAULT '',
    destination_type TEXT NOT NULL DEFAULT '',
    destination_team_key TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (league_id, transaction_key, player_id),
    FOREIGN KEY (league_id, transaction_key)
        REFERENCES yahoo_transactions(league_id, transaction_key) ON DELETE CASCADE
);

CREATE INDEX idx_yahoo_transaction_players_player ON yahoo_transaction_players(player_id);

-- Yahoo Fantasy draft results
CREATE TABLE yahoo_draft_results (
    league_id INT NOT NULL REFERENCES yahoo_leagues(id),
    round INT NOT NULL,
    pick INT NOT NULL,
    team_id INT NOT NULL,
    player_id INT NOT NULL,
    cost INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (league_id, round, pick),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id)
);

-- Yahoo Fantasy matchups
CREATE TABLE yahoo_matchups (
    league_id INT NOT NULL REFERENCES yahoo_leagues(id),
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
    PRIMARY KEY (league_id, week, team1_id, team2_id),
    FOREIGN KEY (league_id, team1_id) REFERENCES yahoo_teams(league_id, id),
    FOREIGN KEY (league_id, team2_id) REFERENCES yahoo_teams(league_id, id)
);
