-- Migrate yahoo_team_summary_stats from EAV to wide columns on yahoo_team_summaries.
--
-- Yahoo stat IDs (stable across all leagues):
--   1=G, 2=A, 3=P, 4=+/-, 5=PIM, 8=PPP, 14=SOG, 16=FW, 17=FL,
--   19=W, 22=GA, 23=GAA, 24=SA, 25=SV, 26=SV%, 27=SHO,
--   29=SHP, 30=GWG, 31=HIT, 32=BLK

-- Step 1: Drop dependent views (cascade from yahoo_season_team_totals → yahoo_roto_standings)
DROP VIEW IF EXISTS yahoo_roto_standings;
DROP VIEW IF EXISTS yahoo_season_team_totals;

-- Step 2: Add typed stat columns to yahoo_team_summaries
ALTER TABLE yahoo_team_summaries
    ADD COLUMN goals REAL,
    ADD COLUMN assists REAL,
    ADD COLUMN points REAL,
    ADD COLUMN plus_minus REAL,
    ADD COLUMN pim REAL,
    ADD COLUMN ppp REAL,
    ADD COLUMN sog REAL,
    ADD COLUMN faceoffs_won REAL,
    ADD COLUMN faceoffs_lost REAL,
    ADD COLUMN wins REAL,
    ADD COLUMN goals_against REAL,
    ADD COLUMN gaa REAL,
    ADD COLUMN shots_against REAL,
    ADD COLUMN saves REAL,
    ADD COLUMN save_pct REAL,
    ADD COLUMN shutouts REAL,
    ADD COLUMN shp REAL,
    ADD COLUMN gwg REAL,
    ADD COLUMN hits REAL,
    ADD COLUMN blocks REAL;

-- Step 3: Backfill from EAV data (pivot stat rows into columns)
UPDATE yahoo_team_summaries s SET
    goals         = sub.goals,
    assists       = sub.assists,
    points        = sub.points,
    plus_minus    = sub.plus_minus,
    pim           = sub.pim,
    ppp           = sub.ppp,
    sog           = sub.sog,
    faceoffs_won  = sub.faceoffs_won,
    faceoffs_lost = sub.faceoffs_lost,
    wins          = sub.wins,
    goals_against = sub.goals_against,
    gaa           = sub.gaa,
    shots_against = sub.shots_against,
    saves         = sub.saves,
    save_pct      = sub.save_pct,
    shutouts      = sub.shutouts,
    shp           = sub.shp,
    gwg           = sub.gwg,
    hits          = sub.hits,
    blocks        = sub.blocks
FROM (
    SELECT
        league_id, team_id, date,
        MAX(CASE WHEN stat_id = 1  AND value <> '-' THEN value::real END) AS goals,
        MAX(CASE WHEN stat_id = 2  AND value <> '-' THEN value::real END) AS assists,
        MAX(CASE WHEN stat_id = 3  AND value <> '-' THEN value::real END) AS points,
        MAX(CASE WHEN stat_id = 4  AND value <> '-' THEN value::real END) AS plus_minus,
        MAX(CASE WHEN stat_id = 5  AND value <> '-' THEN value::real END) AS pim,
        MAX(CASE WHEN stat_id = 8  AND value <> '-' THEN value::real END) AS ppp,
        MAX(CASE WHEN stat_id = 14 AND value <> '-' THEN value::real END) AS sog,
        MAX(CASE WHEN stat_id = 16 AND value <> '-' THEN value::real END) AS faceoffs_won,
        MAX(CASE WHEN stat_id = 17 AND value <> '-' THEN value::real END) AS faceoffs_lost,
        MAX(CASE WHEN stat_id = 19 AND value <> '-' THEN value::real END) AS wins,
        MAX(CASE WHEN stat_id = 22 AND value <> '-' THEN value::real END) AS goals_against,
        MAX(CASE WHEN stat_id = 23 AND value <> '-' THEN value::real END) AS gaa,
        MAX(CASE WHEN stat_id = 24 AND value <> '-' THEN value::real END) AS shots_against,
        MAX(CASE WHEN stat_id = 25 AND value <> '-' THEN value::real END) AS saves,
        MAX(CASE WHEN stat_id = 26 AND value <> '-' THEN value::real END) AS save_pct,
        MAX(CASE WHEN stat_id = 27 AND value <> '-' THEN value::real END) AS shutouts,
        MAX(CASE WHEN stat_id = 29 AND value <> '-' THEN value::real END) AS shp,
        MAX(CASE WHEN stat_id = 30 AND value <> '-' THEN value::real END) AS gwg,
        MAX(CASE WHEN stat_id = 31 AND value <> '-' THEN value::real END) AS hits,
        MAX(CASE WHEN stat_id = 32 AND value <> '-' THEN value::real END) AS blocks
    FROM yahoo_team_summary_stats
    GROUP BY league_id, team_id, date
) sub
WHERE s.league_id = sub.league_id
  AND s.team_id = sub.team_id
  AND s.date = sub.date;

-- Step 4: Drop the EAV table
DROP TABLE yahoo_team_summary_stats;

-- Step 5: Recreate views using wide columns (no more CASE/pivot needed)
CREATE VIEW yahoo_season_team_totals AS
SELECT
    s.league_id,
    s.team_id,
    t.name AS team_name,
    l.season,
    l.num_teams,
    l.scoring_type,
    SUM(COALESCE(s.goals, 0))         AS goals,
    SUM(COALESCE(s.assists, 0))       AS assists,
    SUM(COALESCE(s.plus_minus, 0))    AS plus_minus,
    SUM(COALESCE(s.pim, 0))           AS pim,
    SUM(COALESCE(s.ppp, 0))           AS ppp,
    SUM(COALESCE(s.sog, 0))           AS sog,
    SUM(COALESCE(s.wins, 0))          AS wins,
    SUM(COALESCE(s.goals_against, 0)) AS ga
FROM yahoo_team_summaries s
JOIN yahoo_teams t ON t.league_id = s.league_id AND t.id = s.team_id
JOIN yahoo_leagues l ON l.id = s.league_id
GROUP BY s.league_id, s.team_id, t.name, l.season, l.num_teams, l.scoring_type;

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
