ALTER TABLE season_teams DROP CONSTRAINT IF EXISTS season_teams_nhl_division_required;

-- Backfill international rows (will be dropped) so re-tightening the
-- NOT NULL on division_name/abbrev does not fail.
DELETE FROM season_teams WHERE team_kind = 'international';

ALTER TABLE season_teams ALTER COLUMN division_name SET NOT NULL;
ALTER TABLE season_teams ALTER COLUMN division_abbrev SET NOT NULL;

ALTER TABLE season_teams DROP COLUMN IF EXISTS team_kind;
DROP TYPE IF EXISTS team_kind_enum;
