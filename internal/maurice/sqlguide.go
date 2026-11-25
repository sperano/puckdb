package maurice

// sqlGuide is the compact schema guide appended to the system prompt, so
// a question answered with raw SQL (postgres-mcp's pg_read_query, whose
// tool description is not ours to change) starts from the right table
// instead of information_schema lookups and sample rows. It is sent on
// every request: keep it to the tables Maurice queries most and to facts
// a model cannot guess (ID formats, enum values, where tables disagree).
const sqlGuide = `SQL guide, for raw SQL (pg_read_query); use it instead of exploring information_schema:
- Every season column is an int like 20252026. game_type is an enum: 'regular_season', 'playoffs' (games also has 'all_star' and other events).
- players: id (the NHL player ID, called player_id elsewhere), first_name, last_name, position ('C','LW','RW','D','G'), team_id (current club), birth_date, is_active, draft_year, draft_overall_pick.
- club_skater_stats / club_goalie_stats: NHL season totals, the reference for NHL seasons (the current one included), one row per (season, game_type, team_id, player_id), so a traded player has a row per club: SUM over team_id. Skaters: games_played, goals, assists, points, plus_minus, penalty_minutes, power_play_goals, shots, avg_toi_per_game (seconds). Goalies: games_played, games_started, wins, losses, overtime_losses, saves, shots_against, goals_against_average, save_percentage (0-1), shutouts.
- player_season_totals: career lines of every league (league_abbrev 'NHL', 'AHL', 'OHL', 'KHL', ...), one row per (player_id, season, game_type, league_abbrev, sequence) team stint: SUM per season. Only games_played, goals, assists, points, plus_minus, pim, goalies included. Refreshed rarely: its recent NHL seasons can be partial or missing, so use it for other leagues only and club_* tables for the NHL.
- games: id, season, game_type, game_date, home_team_id, away_team_id, home_team_score, away_team_score, game_state ('FUT', 'LIVE', 'OFF'/'FINAL' = final).
- game_skater_stats / game_goalie_stats: one row per (game_id, player_id) with team_id; join games for season and game_type. Skaters add shots_on_goal, hits, blocked_shots, power_play_points, toi_seconds; goalies decision, saves, shots_against, goals_against, toi_seconds.
- season_teams (season, team_id, abbrev, full_name) names a club in a season.
- An error like "Object 'unknown' not found" usually means a wrong column name (players has id, not player_id).`
