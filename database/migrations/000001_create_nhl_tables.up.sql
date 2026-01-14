-- NHL Conferences table
CREATE TABLE IF NOT EXISTS nhl_conferences (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL
);

-- NHL Divisions table
CREATE TABLE IF NOT EXISTS nhl_divisions (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    nhl_conference_id BIGINT NOT NULL REFERENCES nhl_conferences(id)
);

CREATE INDEX IF NOT EXISTS idx_nhl_divisions_conference ON nhl_divisions(nhl_conference_id);

-- NHL Teams table
CREATE TABLE IF NOT EXISTS nhl_teams (
    id BIGINT PRIMARY KEY,
    yahoo_id BIGINT,
    city TEXT NOT NULL,
    name TEXT NOT NULL,
    abbreviation TEXT NOT NULL,
    nhl_division_id BIGINT NOT NULL REFERENCES nhl_divisions(id),
    nhl_home_link TEXT NOT NULL DEFAULT '',
    yahoo_home_link TEXT NOT NULL DEFAULT '',
    small_logo_url TEXT NOT NULL DEFAULT '',
    large_logo_url TEXT NOT NULL DEFAULT '',
    all_stars BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_nhl_teams_yahoo_id ON nhl_teams(yahoo_id);

CREATE INDEX IF NOT EXISTS idx_nhl_teams_division ON nhl_teams(nhl_division_id);
CREATE INDEX IF NOT EXISTS idx_nhl_teams_all_stars ON nhl_teams(all_stars);
