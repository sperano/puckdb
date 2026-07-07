-- Turn telemetry first (FK into sim_transactions and sim_agents).
DROP TABLE IF EXISTS sim_agent_turn_messages;
DROP TABLE IF EXISTS sim_agent_tool_calls;
DROP TABLE IF EXISTS sim_agent_turn_rounds;
DROP TABLE IF EXISTS sim_agent_turns;

DROP TABLE IF EXISTS sim_lineup_moves;
DROP TABLE IF EXISTS sim_transactions;
DROP TABLE IF EXISTS sim_standings;
DROP TABLE IF EXISTS sim_agent_totals;
DROP TABLE IF EXISTS sim_agent_daily_stats;
DROP TABLE IF EXISTS sim_agent_daily_player_stats;
DROP TABLE IF EXISTS sim_waiver_claims;
DROP TABLE IF EXISTS sim_waiver_priority;
DROP TABLE IF EXISTS sim_rosters;
DROP TABLE IF EXISTS sim_agents;
DROP TABLE IF EXISTS sim_pools;
