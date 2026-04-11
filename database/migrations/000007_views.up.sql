-- Fantasy analysis views

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
JOIN games g ON g.id = gss.game_id AND g.game_type = 'regular_season'
JOIN players p ON p.id = gss.player_id
GROUP BY gss.player_id, p.first_name, p.last_name, p.yahoo_id, p.position, p.team_id, g.season;

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
JOIN games g ON g.id = ggs.game_id AND g.game_type = 'regular_season'
JOIN players p ON p.id = ggs.player_id
WHERE ggs.toi_seconds >= 300
GROUP BY ggs.player_id, p.first_name, p.last_name, p.yahoo_id, p.team_id, g.season;

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
JOIN games g ON g.id = gss.game_id AND g.game_type = 'regular_season'
JOIN players p ON p.id = gss.player_id
WHERE g.game_date >= CURRENT_DATE - INTERVAL '30 days'
GROUP BY gss.player_id, p.first_name, p.last_name, p.yahoo_id, p.position, p.team_id, g.season;

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
JOIN games g ON g.id = ggs.game_id AND g.game_type = 'regular_season'
JOIN players p ON p.id = ggs.player_id
WHERE ggs.toi_seconds >= 300
  AND g.game_date >= CURRENT_DATE - INTERVAL '30 days'
GROUP BY ggs.player_id, p.first_name, p.last_name, p.yahoo_id, p.team_id, g.season;
