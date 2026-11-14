package database

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	toolArgumentsVersion   = 31
	simValidArguments      = `{"player_id":1}`
	mauriceValidArguments  = `{"query":"ok"}`
	jsonNullEscapeArgument = `{"value":"\u0000"}`
)

func seedToolArgumentRows(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	seedSimToolArgumentRows(t, conn)
	seedMauriceToolArgumentRows(t, conn)
}

func seedSimToolArgumentRows(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	execSQL(t, conn, `
WITH season AS (
    INSERT INTO seasons (id, standings_start, standings_end)
    VALUES (20242025, DATE '2024-10-04', DATE '2025-04-17') RETURNING id
), pool AS (
    INSERT INTO sim_pools (name, season, num_teams, waiver_days, draft_rounds, max_llm_cost_usd_per_pool, categories)
    SELECT 'arguments', id, 1, 1, 1, 0, ARRAY['G'] FROM season RETURNING id
), agent AS (
    INSERT INTO sim_agents (pool_id, provider, model, strategy, team_name)
    SELECT id, 'anthropic', 'model', 'balanced', 'Arguments' FROM pool RETURNING id, pool_id
), turn_row AS (
    INSERT INTO sim_agent_turns (pool_id, agent_id, sim_date, phase, status, provider, model, started_at, completed_at)
    SELECT pool_id, id, DATE '2024-10-04', 'daily', 'ok', 'anthropic', 'model', now(), now() FROM agent RETURNING id
)
INSERT INTO sim_agent_tool_calls
    (turn_id, round_index, sequence, tool_name, arguments_raw, arguments, result, outcome)
SELECT id, 0, 0, 'valid', $1::text, ($1::text)::jsonb, '', 'accepted' FROM turn_row
UNION ALL
SELECT id, 0, 1, 'invalid', '{"player_id":', NULL, '', 'parse_error' FROM turn_row
UNION ALL
SELECT id, 0, 2, 'json-null-escape', $2::text, NULL, '', 'parse_error' FROM turn_row`,
		simValidArguments, jsonNullEscapeArgument)
}

func seedMauriceToolArgumentRows(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	execSQL(t, conn, `
WITH user_row AS (
    INSERT INTO app_users (instance, auth_provider, auth_subject, username)
    VALUES ('puckdb', 'authentik', 'arguments', 'arguments') RETURNING id
), conversation AS (
    INSERT INTO maurice_conversations (user_id) SELECT id FROM user_row RETURNING id, user_id
), turn_row AS (
    INSERT INTO maurice_turns
        (conversation_id, user_id, turn_number, idempotency_key, request_hash, status, started_at, completed_at)
    SELECT id, user_id, 0, 'arguments', decode('00', 'hex'), 'succeeded', now(), now() FROM conversation
    RETURNING id, conversation_id
), call_row AS (
    INSERT INTO maurice_llm_calls
        (turn_id, conversation_id, call_kind, round_number, provider, model, status, started_at, completed_at)
    SELECT id, conversation_id, 'chat_round', 0, 'anthropic', 'model', 'succeeded', now(), now() FROM turn_row
    RETURNING id, turn_id
)
INSERT INTO maurice_tool_calls
    (turn_id, llm_call_id, sequence_number, tool_name, arguments_raw, arguments, result, status, started_at, completed_at)
SELECT turn_id, id, 0, 'valid', $1::text, ($1::text)::jsonb, '', 'succeeded', now(), now() FROM call_row
UNION ALL
SELECT turn_id, id, 1, 'invalid', '{"query":', NULL, '', 'parse_error', now(), now() FROM call_row
UNION ALL
SELECT turn_id, id, 2, 'json-null-escape', $2::text, NULL, '', 'parse_error', now(), now() FROM call_row`,
		mauriceValidArguments, jsonNullEscapeArgument)
}

func assertRestoredToolArguments(t *testing.T, conn *pgx.Conn, table, validArguments string) {
	t.Helper()
	rows, err := conn.Query(context.Background(),
		`SELECT tool_name, arguments::text FROM `+table)
	require.NoError(t, err)
	defer rows.Close()

	arguments := make(map[string]*string)
	for rows.Next() {
		var toolName string
		var value *string
		require.NoError(t, rows.Scan(&toolName, &value))
		arguments[toolName] = value
	}
	require.NoError(t, rows.Err())

	require.Contains(t, arguments, "valid")
	require.Contains(t, arguments, "invalid")
	require.Contains(t, arguments, "json-null-escape")
	require.NotNil(t, arguments["valid"])
	assert.JSONEq(t, validArguments, *arguments["valid"])
	assert.Nil(t, arguments["invalid"])
	assert.Nil(t, arguments["json-null-escape"])
}

// Up removes the duplicate parsed payloads. Down derives them for valid jsonb
// without rejecting the malformed provider output that raw text preserves.
func TestIntegration_ToolArgumentsMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, toolArgumentsVersion-1)
	seedToolArgumentRows(t, conn)

	migrateTo(t, dbURL, toolArgumentsVersion)
	for _, table := range []string{"sim_agent_tool_calls", "maurice_tool_calls"} {
		exists, _ := columnNullable(t, conn, table, "arguments")
		assert.False(t, exists)
	}

	migrateTo(t, dbURL, toolArgumentsVersion-1)
	for _, table := range []string{"sim_agent_tool_calls", "maurice_tool_calls"} {
		exists, nullable := columnNullable(t, conn, table, "arguments")
		assert.True(t, exists)
		assert.True(t, nullable)
	}
	assertRestoredToolArguments(t, conn, "sim_agent_tool_calls", simValidArguments)
	assertRestoredToolArguments(t, conn, "maurice_tool_calls", mauriceValidArguments)

	migrateTo(t, dbURL, toolArgumentsVersion)
}
