-- nhl-baseline-v6 builds on v5 (aging curve, team environment) and counts a
-- goalie's games as the games he played in rather than every boxscore he was
-- dressed in (see internal/projection/goalie.go). It fits and stores its aging
-- curve like v4 and v5, so it joins the constraint requiring one.
ALTER TABLE projection_snapshots
    DROP CONSTRAINT projection_snapshots_aging_curve_check,
    ADD CONSTRAINT projection_snapshots_aging_curve_check
        CHECK (model_version NOT IN ('nhl-baseline-v4', 'nhl-baseline-v5', 'nhl-baseline-v6')
            OR aging_curve IS NOT NULL);
