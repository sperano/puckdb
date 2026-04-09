-- Reverse: recreate the EAV table and restore data from wide columns.

-- Step 1: Drop views that reference the wide columns
DROP VIEW IF EXISTS yahoo_roto_standings;
DROP VIEW IF EXISTS yahoo_season_team_totals;

-- Step 2: Recreate the EAV table
CREATE TABLE IF NOT EXISTS yahoo_team_summary_stats (
    league_id INT NOT NULL,
    team_id INT NOT NULL,
    date DATE NOT NULL,
    stat_id INT NOT NULL,
    value TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (league_id, team_id, date, stat_id),
    FOREIGN KEY (league_id, team_id, date) REFERENCES yahoo_team_summaries(league_id, team_id, date) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_yahoo_team_summary_stats_stat
    ON yahoo_team_summary_stats(stat_id);

-- Step 3: Repopulate EAV from wide columns
INSERT INTO yahoo_team_summary_stats (league_id, team_id, date, stat_id, value)
SELECT league_id, team_id, date, stat_id, value::text
FROM yahoo_team_summaries
CROSS JOIN LATERAL (
    VALUES
        (1, goals), (2, assists), (3, points), (4, plus_minus), (5, pim),
        (8, ppp), (14, sog), (16, faceoffs_won), (17, faceoffs_lost),
        (19, wins), (22, goals_against), (23, gaa), (24, shots_against),
        (25, saves), (26, save_pct), (27, shutouts), (29, shp), (30, gwg),
        (31, hits), (32, blocks)
) AS v(stat_id, value)
WHERE value IS NOT NULL;

-- Step 4: Drop wide columns
ALTER TABLE yahoo_team_summaries
    DROP COLUMN goals,
    DROP COLUMN assists,
    DROP COLUMN points,
    DROP COLUMN plus_minus,
    DROP COLUMN pim,
    DROP COLUMN ppp,
    DROP COLUMN sog,
    DROP COLUMN faceoffs_won,
    DROP COLUMN faceoffs_lost,
    DROP COLUMN wins,
    DROP COLUMN goals_against,
    DROP COLUMN gaa,
    DROP COLUMN shots_against,
    DROP COLUMN saves,
    DROP COLUMN save_pct,
    DROP COLUMN shutouts,
    DROP COLUMN shp,
    DROP COLUMN gwg,
    DROP COLUMN hits,
    DROP COLUMN blocks;

-- Step 5: Recreate original views using EAV table
CREATE VIEW yahoo_season_team_totals AS
SELECT
    ss.league_id,
    ss.team_id,
    t.name AS team_name,
    l.season,
    l.num_teams,
    l.scoring_type,
    SUM(CASE WHEN ss.stat_id = 1  AND ss.value <> '-' THEN ss.value::numeric ELSE 0 END) AS goals,
    SUM(CASE WHEN ss.stat_id = 2  AND ss.value <> '-' THEN ss.value::numeric ELSE 0 END) AS assists,
    SUM(CASE WHEN ss.stat_id = 4  AND ss.value <> '-' THEN ss.value::numeric ELSE 0 END) AS plus_minus,
    SUM(CASE WHEN ss.stat_id = 5  AND ss.value <> '-' THEN ss.value::numeric ELSE 0 END) AS pim,
    SUM(CASE WHEN ss.stat_id = 8  AND ss.value <> '-' THEN ss.value::numeric ELSE 0 END) AS ppp,
    SUM(CASE WHEN ss.stat_id = 14 AND ss.value <> '-' THEN ss.value::numeric ELSE 0 END) AS sog,
    SUM(CASE WHEN ss.stat_id = 19 AND ss.value <> '-' THEN ss.value::numeric ELSE 0 END) AS wins,
    SUM(CASE WHEN ss.stat_id = 22 AND ss.value <> '-' THEN ss.value::numeric ELSE 0 END) AS ga
FROM yahoo_team_summary_stats ss
JOIN yahoo_teams t ON t.league_id = ss.league_id AND t.id = ss.team_id
JOIN yahoo_leagues l ON l.id = ss.league_id
GROUP BY ss.league_id, ss.team_id, t.name, l.season, l.num_teams, l.scoring_type;

CREATE VIEW yahoo_roto_standings AS
WITH ranked AS (
    SELECT
        st.*,
        RANK() OVER (PARTITION BY st.league_id ORDER BY st.goals DESC)      AS g_rank,
        RANK() OVER (PARTITION BY st.league_id ORDER BY st.assists DESC)    AS a_rank,
        RANK() OVER (PARTITION BY st.league_id ORDER BY st.plus_minus DESC) AS pm_rank,
        RANK() OVER (PARTITION BY st.league_id ORDER BY st.pim DESC)        AS pim_rank,
        RANK() OVER (PARTITION BY st.league_id ORDER BY st.ppp DESC)        AS ppp_rank,
        RANK() OVER (PARTITION BY st.league_id ORDER BY st.sog DESC)        AS sog_rank,
        RANK() OVER (PARTITION BY st.league_id ORDER BY st.wins DESC)       AS w_rank,
        RANK() OVER (PARTITION BY st.league_id ORDER BY st.ga ASC)          AS ga_rank
    FROM yahoo_season_team_totals st
    WHERE st.scoring_type = 'roto'
)
SELECT
    r.league_id,
    r.team_id,
    r.team_name,
    r.season,
    r.num_teams,
    r.goals,      (r.num_teams + 1 - r.g_rank)::int   AS g_pts,
    r.assists,    (r.num_teams + 1 - r.a_rank)::int    AS a_pts,
    r.plus_minus, (r.num_teams + 1 - r.pm_rank)::int   AS pm_pts,
    r.pim,        (r.num_teams + 1 - r.pim_rank)::int  AS pim_pts,
    r.ppp,        (r.num_teams + 1 - r.ppp_rank)::int  AS ppp_pts,
    r.sog,        (r.num_teams + 1 - r.sog_rank)::int  AS sog_pts,
    r.wins,       (r.num_teams + 1 - r.w_rank)::int    AS w_pts,
    r.ga,         (r.num_teams + 1 - r.ga_rank)::int   AS ga_pts,
    (r.num_teams + 1 - r.g_rank)  +
    (r.num_teams + 1 - r.a_rank)  +
    (r.num_teams + 1 - r.pm_rank) +
    (r.num_teams + 1 - r.pim_rank) +
    (r.num_teams + 1 - r.ppp_rank) +
    (r.num_teams + 1 - r.sog_rank) +
    (r.num_teams + 1 - r.w_rank)  +
    (r.num_teams + 1 - r.ga_rank)  AS total_roto_pts
FROM ranked r
ORDER BY r.league_id, total_roto_pts DESC;
