-- Fantasy analysis views
-- Bridges Yahoo Fantasy data with NHL game statistics for roster analysis

-- =============================================================================
-- View: yahoo_roster_players
-- =============================================================================
-- Joins Yahoo Fantasy rosters with the NHL players table via yahoo_id.
-- This is the bridge between the two ID systems: Yahoo uses its own player IDs,
-- while game stats reference NHL API player IDs. Without this view, correlating
-- "who is on a fantasy roster" with "how are they performing in real games"
-- requires manual ID translation every time.
--
-- Columns exposed: all roster fields + player name, NHL ID, position, team,
-- headshot, active status.

CREATE VIEW yahoo_roster_players AS
SELECT
    r.league_id,
    r.team_id,
    r.date,
    r.player_id AS yahoo_player_id,
    r.player_key,
    r.selected_position,
    r.is_flex,
    r.coverage_type,
    r.is_editable,
    p.id AS nhl_player_id,
    p.first_name,
    p.last_name,
    p.position AS nhl_position,
    p.team_id AS nhl_team_id,
    p.is_active,
    p.headshot_url
FROM yahoo_team_rosters r
LEFT JOIN players p ON p.yahoo_id = r.player_id;

-- =============================================================================
-- View: yahoo_season_team_totals
-- =============================================================================
-- Aggregates daily yahoo_team_summary_stats into season-level cumulative totals
-- per team. The raw data stores one row per (team, date, stat), so answering
-- "how many goals does team X have this season?" requires summing across all
-- dates. This view pre-computes that pivot.
--
-- Yahoo stores stat values as TEXT (because some are "-" when a team has no
-- goalie playing that day). This view casts valid numeric values and ignores
-- dashes. It pivots the EAV (entity-attribute-value) stat rows into proper
-- columns using the stat_id mapping from yahoo_league_stat_categories.
--
-- The stat_ids are Yahoo-global constants (not league-specific), so the pivot
-- is stable across leagues:
--   1=G, 2=A, 4=+/-, 5=PIM, 8=PPP, 14=SOG, 19=W, 22=GA, 23=GAA
--
-- GAA is excluded from summing because it's a rate stat — you can derive it
-- from GA and games played if needed.

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

-- =============================================================================
-- View: yahoo_roto_standings
-- =============================================================================
-- Computes full roto standings from season totals. In a rotisserie league,
-- each team is ranked 1..N in every scoring category. The rank IS the score:
-- rank 1 (best) earns N points, rank N (worst) earns 1 point. The team with
-- the highest total across all categories wins.
--
-- This view applies RANK() window functions to each category. "Higher is
-- better" categories (G, A, +/-, PIM, PPP, SOG, W) are ranked DESC, while
-- "lower is better" categories (GA) are ranked ASC. The roto_points for each
-- category = (num_teams + 1 - rank), so in a 12-team league, 1st place = 12 pts.
--
-- Ties: RANK() leaves gaps (e.g., two teams tied for 3rd both get rank 3,
-- next team gets rank 5). This matches Yahoo's roto tie-breaking behavior.
--
-- Only includes leagues with scoring_type = 'roto'.

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

-- =============================================================================
-- View: skater_season_stats
-- =============================================================================
-- Full-season NHL stats per skater for completed regular-season games.
-- Joins game_skater_stats with games (for season/date filtering) and players
-- (for name/position). This replaces the need to manually join three tables
-- every time you want to know "how is player X doing this year?"
--
-- Includes both counting stats (G, A, SOG, PIM, PPP, hits, blocks) and
-- contextual info (team, games played, TOI per game).

CREATE VIEW skater_season_stats AS
SELECT
    gss.player_id,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    p.position,
    p.team_id AS current_team_id,
    g.season,
    COUNT(DISTINCT gss.game_id)     AS gp,
    SUM(gss.goals)                  AS goals,
    SUM(gss.assists)                AS assists,
    SUM(gss.points)                 AS points,
    SUM(gss.plus_minus)             AS plus_minus,
    SUM(gss.penalty_minutes)        AS pim,
    SUM(gss.shots_on_goal)          AS sog,
    SUM(gss.power_play_points)      AS ppp,
    SUM(gss.power_play_goals)       AS ppg,
    SUM(gss.hits)                   AS hits,
    SUM(gss.blocked_shots)          AS blocks,
    ROUND(SUM(gss.toi_seconds)::numeric / NULLIF(COUNT(DISTINCT gss.game_id), 0) / 60, 1) AS avg_toi_min
FROM game_skater_stats gss
JOIN games g ON g.id = gss.game_id AND g.game_type = 2
JOIN players p ON p.id = gss.player_id
GROUP BY gss.player_id, p.first_name, p.last_name, p.yahoo_id, p.position, p.team_id, g.season;

-- =============================================================================
-- View: goalie_season_stats
-- =============================================================================
-- Full-season NHL stats per goalie. Computes derived rate stats (GAA, SV%)
-- from the raw counting stats. Only includes appearances where the goalie
-- played at least 5 minutes (300 seconds) to filter out pulled-goalie
-- artifacts where a skater briefly enters the crease.
--
-- GAA = (goals_against / time_on_ice_seconds) * 3600
-- SV% = saves / shots_against

CREATE VIEW goalie_season_stats AS
SELECT
    ggs.player_id,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    p.team_id AS current_team_id,
    g.season,
    COUNT(DISTINCT ggs.game_id)                                       AS gp,
    SUM(CASE WHEN ggs.decision = 'W' THEN 1 ELSE 0 END)              AS wins,
    SUM(CASE WHEN ggs.decision = 'L' THEN 1 ELSE 0 END)              AS losses,
    SUM(ggs.goals_against)                                            AS ga,
    SUM(ggs.saves)                                                    AS saves,
    SUM(ggs.shots_against)                                            AS shots_against,
    ROUND(SUM(ggs.goals_against)::numeric
        / NULLIF(SUM(ggs.toi_seconds), 0) * 3600, 2)                 AS gaa,
    ROUND(SUM(ggs.saves)::numeric
        / NULLIF(SUM(ggs.shots_against), 0), 3)                      AS sv_pct
FROM game_goalie_stats ggs
JOIN games g ON g.id = ggs.game_id AND g.game_type = 2
JOIN players p ON p.id = ggs.player_id
WHERE ggs.toi_seconds >= 300
GROUP BY ggs.player_id, p.first_name, p.last_name, p.yahoo_id, p.team_id, g.season;

-- =============================================================================
-- View: skater_recent_stats
-- =============================================================================
-- Rolling 30-day skater production. Same structure as skater_season_stats but
-- filtered to games from the last 30 days. This answers "who's hot right now?"
-- without needing to remember the date arithmetic and joins.
--
-- Uses CURRENT_DATE so it's always relative to when you query it.

CREATE VIEW skater_recent_stats AS
SELECT
    gss.player_id,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    p.position,
    p.team_id AS current_team_id,
    g.season,
    COUNT(DISTINCT gss.game_id)     AS gp,
    SUM(gss.goals)                  AS goals,
    SUM(gss.assists)                AS assists,
    SUM(gss.points)                 AS points,
    SUM(gss.plus_minus)             AS plus_minus,
    SUM(gss.penalty_minutes)        AS pim,
    SUM(gss.shots_on_goal)          AS sog,
    SUM(gss.power_play_points)      AS ppp,
    SUM(gss.power_play_goals)       AS ppg,
    SUM(gss.hits)                   AS hits,
    SUM(gss.blocked_shots)          AS blocks,
    ROUND(SUM(gss.toi_seconds)::numeric / NULLIF(COUNT(DISTINCT gss.game_id), 0) / 60, 1) AS avg_toi_min
FROM game_skater_stats gss
JOIN games g ON g.id = gss.game_id AND g.game_type = 2
JOIN players p ON p.id = gss.player_id
WHERE g.game_date >= CURRENT_DATE - INTERVAL '30 days'
GROUP BY gss.player_id, p.first_name, p.last_name, p.yahoo_id, p.position, p.team_id, g.season;

-- =============================================================================
-- View: goalie_recent_stats
-- =============================================================================
-- Rolling 30-day goalie production. Same filtering logic as skater_recent_stats.
-- Particularly useful for identifying streaming targets — goalies on hot streaks
-- who might be available on waivers.

CREATE VIEW goalie_recent_stats AS
SELECT
    ggs.player_id,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    p.team_id AS current_team_id,
    g.season,
    COUNT(DISTINCT ggs.game_id)                                       AS gp,
    SUM(CASE WHEN ggs.decision = 'W' THEN 1 ELSE 0 END)              AS wins,
    SUM(CASE WHEN ggs.decision = 'L' THEN 1 ELSE 0 END)              AS losses,
    SUM(ggs.goals_against)                                            AS ga,
    SUM(ggs.saves)                                                    AS saves,
    SUM(ggs.shots_against)                                            AS shots_against,
    ROUND(SUM(ggs.goals_against)::numeric
        / NULLIF(SUM(ggs.toi_seconds), 0) * 3600, 2)                 AS gaa,
    ROUND(SUM(ggs.saves)::numeric
        / NULLIF(SUM(ggs.shots_against), 0), 3)                      AS sv_pct
FROM game_goalie_stats ggs
JOIN games g ON g.id = ggs.game_id AND g.game_type = 2
JOIN players p ON p.id = ggs.player_id
WHERE ggs.toi_seconds >= 300
  AND g.game_date >= CURRENT_DATE - INTERVAL '30 days'
GROUP BY ggs.player_id, p.first_name, p.last_name, p.yahoo_id, p.team_id, g.season;
