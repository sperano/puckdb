-- =============================================================================
-- Team Schedule Queries (games per team in a date range, for streaming)
-- =============================================================================
-- A game counts unless it is postponed, cancelled or not to be played on
-- its date (game_schedule_state PPD, CNCL, DONT_PLAY): a postponed game
-- gets a new game_date when it is rescheduled.

-- name: ListScheduleNights :many
-- League-wide number of games per date in [start_date, end_date] for one
-- game type; dates without a game are absent.
SELECT game_date, count(*)::int AS games
FROM games
WHERE game_date BETWEEN @start_date::date AND @end_date::date
  AND game_type = @game_type::game_type
  AND game_schedule_state NOT IN ('PPD', 'CNCL', 'DONT_PLAY')
GROUP BY game_date
ORDER BY game_date;

-- name: GetTeamScheduleCounts :many
-- Games per team in [start_date, end_date] for one game type: home/away,
-- back-to-backs (games the day after another game of the team, both in the
-- range) and games on light/heavy nights (league-wide game count at most
-- light_night_max_games / at least heavy_night_min_games). For the regular
-- season, every NHL club of a season whose standings window overlaps the
-- range is listed, with 0 games when it has none (a bye week); otherwise
-- only teams with a game are. team_abbrev comes from the latest such season.
WITH counted AS (
    SELECT g.season, g.game_date, g.home_team_id, g.away_team_id
    FROM games g
    WHERE g.game_date BETWEEN @start_date::date AND @end_date::date
      AND g.game_type = @game_type::game_type
      AND g.game_schedule_state NOT IN ('PPD', 'CNCL', 'DONT_PLAY')
),
nights AS (
    SELECT game_date, count(*) AS games FROM counted GROUP BY game_date
),
team_games AS (
    SELECT season, game_date, home_team_id AS team_id, true AS is_home FROM counted
    UNION ALL
    SELECT season, game_date, away_team_id AS team_id, false AS is_home FROM counted
),
sequenced AS (
    SELECT tg.team_id, tg.game_date, tg.is_home,
        lag(tg.game_date) OVER (PARTITION BY tg.team_id ORDER BY tg.game_date) AS previous_date
    FROM team_games tg
),
listed AS (
    SELECT st.season, st.team_id
    FROM season_teams st
    JOIN seasons s ON s.id = st.season
    WHERE @game_type::game_type = 'regular_season'
      AND st.team_kind = 'nhl'
      AND s.standings_start <= @end_date::date
      AND s.standings_end >= @start_date::date
    UNION
    SELECT season, team_id FROM team_games
),
teams AS (
    SELECT l.team_id,
        (SELECT st.abbrev FROM season_teams st
         WHERE st.team_id = l.team_id AND st.season IN (SELECT season FROM listed)
         ORDER BY st.season DESC LIMIT 1) AS team_abbrev
    FROM listed l
    WHERE sqlc.narg('team_id')::bigint IS NULL OR l.team_id = sqlc.narg('team_id')::bigint
    GROUP BY l.team_id
)
SELECT t.team_id,
    COALESCE(t.team_abbrev, '')::text AS team_abbrev,
    count(sq.game_date)::int AS games,
    count(*) FILTER (WHERE sq.is_home)::int AS home,
    count(*) FILTER (WHERE NOT sq.is_home)::int AS away,
    count(*) FILTER (WHERE sq.previous_date = sq.game_date - 1)::int AS back_to_backs,
    count(*) FILTER (WHERE n.games <= @light_night_max_games::int)::int AS light_night_games,
    count(*) FILTER (WHERE n.games >= @heavy_night_min_games::int)::int AS heavy_night_games,
    COALESCE(string_agg(to_char(sq.game_date, 'YYYY-MM-DD'), ';' ORDER BY sq.game_date), '')::text AS game_dates
FROM teams t
LEFT JOIN sequenced sq ON sq.team_id = t.team_id
LEFT JOIN nights n ON n.game_date = sq.game_date
GROUP BY t.team_id, t.team_abbrev
ORDER BY games DESC, light_night_games DESC, team_abbrev, t.team_id;
