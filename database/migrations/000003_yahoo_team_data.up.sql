-- Yahoo Team Data: summaries (wide columns) and rosters (enriched)

CREATE TABLE yahoo_team_summaries (
    league_id INT NOT NULL,
    team_id INT NOT NULL,
    date DATE NOT NULL,
    coverage_type TEXT NOT NULL DEFAULT 'date',
    -- Wide stat columns (Yahoo stat IDs: 1=G, 2=A, 3=P, 4=+/-, 5=PIM, etc.)
    goals REAL,
    assists REAL,
    points REAL,
    plus_minus REAL,
    pim REAL,
    ppp REAL,
    sog REAL,
    faceoffs_won REAL,
    faceoffs_lost REAL,
    wins REAL,
    goals_against REAL,
    gaa REAL,
    shots_against REAL,
    saves REAL,
    save_pct REAL,
    shutouts REAL,
    shp REAL,
    gwg REAL,
    hits REAL,
    blocks REAL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (league_id, team_id, date),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_yahoo_team_summaries_date ON yahoo_team_summaries(date);

CREATE TABLE yahoo_team_rosters (
    league_id INT NOT NULL,
    team_id INT NOT NULL,
    date DATE NOT NULL,
    player_id INT NOT NULL,
    coverage_type TEXT NOT NULL DEFAULT 'date',
    is_editable BOOLEAN NOT NULL DEFAULT FALSE,
    player_key TEXT NOT NULL DEFAULT '',
    selected_position TEXT NOT NULL DEFAULT '',
    is_flex BOOLEAN NOT NULL DEFAULT FALSE,
    -- Player detail enrichment
    player_status TEXT,
    player_status_full TEXT,
    injury_note TEXT,
    on_disabled_list BOOLEAN,
    position_type TEXT,
    display_position TEXT,
    primary_position TEXT,
    eligible_positions TEXT[],
    uniform_number INT,
    editorial_team_abbr TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (league_id, team_id, date, player_id),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_yahoo_team_rosters_date ON yahoo_team_rosters(date);
CREATE INDEX idx_yahoo_team_rosters_player ON yahoo_team_rosters(player_id);
