-- Distinguish NHL teams from international/national teams that show up
-- in player_season_totals (Canada, USA, Sweden, ... at IDs 60–67) for
-- WJC/Olympic/World Cup tournaments. Without this, the
-- player_season_totals_season_team_id_fkey FK rejects entire batches
-- of player career totals when they include international rows,
-- because season_teams previously held only NHL franchises.
CREATE TYPE team_kind_enum AS ENUM ('nhl', 'international');

ALTER TABLE season_teams
    ADD COLUMN team_kind team_kind_enum NOT NULL DEFAULT 'nhl';

-- division_name and division_abbrev are NOT NULL, but international
-- teams have neither. Make them nullable for international rows
-- (NHL rows still required to set them).
ALTER TABLE season_teams ALTER COLUMN division_name DROP NOT NULL;
ALTER TABLE season_teams ALTER COLUMN division_abbrev DROP NOT NULL;
ALTER TABLE season_teams ADD CONSTRAINT season_teams_nhl_division_required
    CHECK (team_kind <> 'nhl' OR (division_name IS NOT NULL AND division_abbrev IS NOT NULL));
