package database

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const toolCallNormalizationVersion = 31

const normalizedCallsJSON = `[
  {"id":"call-1","type":"function","function":{"name":"valid","arguments":"{\"player_id\":1}"}},
  {"id":"call-2","type":"function","function":{"name":"invalid","arguments":"{\"player_id\":"}},
  {"id":"call-3","type":"function","function":{"name":"json-null","arguments":"{\"value\":\"\\u0000\"}"}},
  {"id":"call-4","type":"function","function":{"name":"transcript-only","arguments":"{}"}},
  {"id":"call-5","type":"function","function":{"name":"surrogate","arguments":"{\"value\":\"\\ud800\"}"}}
]`

const normalizedSimulationCallsJSON = `[
  {"id":"call-1","type":"function","function":{"name":"valid","arguments":"{\"player_id\":1}"}},
  {"id":"call-2","type":"function","function":{"name":"invalid","arguments":"{\"player_id\":"}},
  {"id":"call-3","type":"function","function":{"name":"json-null","arguments":"{\"value\":\"\\u0000\"}"}},
  {"id":"call-4","type":"function","function":{"name":"surrogate","arguments":"{\"value\":\"\\ud800\"}"}}
]`

const repeatedProviderCallJSON = `[
  {"id":"call-1","type":"function","function":{"name":"second","arguments":"{\"player_id\":2}"}}
]`

func seedNormalizedToolCalls(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	seedSimulationTranscript(t, conn)
	seedMauriceTranscript(t, conn)
}

func seedSimulationTranscript(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	execSQL(t, conn, `
WITH season AS (
    INSERT INTO seasons (id, standings_start, standings_end)
    VALUES (20242025, DATE '2024-10-04', DATE '2025-04-17') RETURNING id
), pool AS (
    INSERT INTO sim_pools (name, season, num_teams, waiver_days, draft_rounds, max_llm_cost_usd_per_pool, categories)
    SELECT 'normalization', id, 1, 1, 1, 0, ARRAY['G'] FROM season RETURNING id
), agent AS (
    INSERT INTO sim_agents (pool_id, provider, model, strategy, team_name)
    SELECT id, 'anthropic', 'model', 'balanced', 'Normalization' FROM pool RETURNING id, pool_id
), turn_row AS (
    INSERT INTO sim_agent_turns (pool_id, agent_id, sim_date, phase, status, provider, model, started_at, completed_at)
    SELECT pool_id, id, DATE '2024-10-04', 'daily', 'ok', 'anthropic', 'model', now(), now() FROM agent RETURNING id
), messages AS (
    INSERT INTO sim_agent_turn_messages (turn_id, ordinal, role, content, tool_call_id, tool_calls)
    SELECT id, 0, 'assistant', '', NULL, $1::jsonb FROM turn_row
    UNION ALL
    SELECT id, 1, 'tool', 'done', 'call-1', NULL FROM turn_row
    UNION ALL
    SELECT id, 2, 'assistant', '', NULL, $2::jsonb FROM turn_row
    UNION ALL
    SELECT id, 3, 'tool', 'done again', 'call-1', NULL FROM turn_row
)
INSERT INTO sim_agent_tool_calls
    (turn_id, round_index, sequence, tool_name, arguments_raw, arguments, result, outcome)
SELECT id, 0, 0, 'valid', '{"player_id":1}', '{"player_id":1}'::jsonb, 'done', 'accepted' FROM turn_row
UNION ALL
SELECT id, 0, 1, 'invalid', '{"player_id":', NULL, '', 'parse_error' FROM turn_row
UNION ALL
SELECT id, 0, 2, 'json-null', '{"value":"\u0000"}', NULL, '', 'parse_error' FROM turn_row
UNION ALL
SELECT id, 0, 3, 'surrogate', '{"value":"\ud800"}', NULL, '', 'parse_error' FROM turn_row
UNION ALL
SELECT id, 1, 0, 'second', '{"player_id":2}', '{"player_id":2}'::jsonb, 'done again', 'accepted' FROM turn_row`,
		normalizedSimulationCallsJSON, repeatedProviderCallJSON)
}

func seedMauriceTranscript(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	execSQL(t, conn, `
WITH user_row AS (
    INSERT INTO app_users (instance, auth_provider, auth_subject, username)
    VALUES ('puckdb', 'authentik', 'normalization', 'normalization') RETURNING id
), conversation AS (
    INSERT INTO maurice_conversations (user_id) SELECT id FROM user_row RETURNING id, user_id
), turn_row AS (
    INSERT INTO maurice_turns
        (conversation_id, user_id, turn_number, idempotency_key, request_hash, status, started_at, completed_at)
    SELECT id, user_id, 0, 'normalization', decode('00', 'hex'), 'succeeded', now(), now() FROM conversation
    RETURNING id, conversation_id
), messages AS (
    INSERT INTO maurice_messages (conversation_id, turn_id, message_number, role, content, tool_call_id, tool_calls)
    SELECT conversation_id, id, 0, 'assistant'::chat_role, '', NULL, $1::jsonb FROM turn_row
    UNION ALL
    SELECT conversation_id, id, 1, 'tool'::chat_role, 'done', 'call-1', NULL FROM turn_row
    UNION ALL
    SELECT conversation_id, id, 2, 'assistant'::chat_role, '', NULL, $2::jsonb FROM turn_row
    UNION ALL
    SELECT conversation_id, id, 3, 'tool'::chat_role, 'done again', 'call-1', NULL FROM turn_row
), call_row AS (
    INSERT INTO maurice_llm_calls
        (turn_id, conversation_id, call_kind, round_number, provider, model, status, started_at, completed_at)
    SELECT id, conversation_id, 'chat_round', round_number, 'anthropic', 'model', 'succeeded', now(), now()
    FROM turn_row CROSS JOIN generate_series(0, 1) AS rounds(round_number)
    RETURNING id, turn_id, round_number
)
INSERT INTO maurice_tool_calls
    (turn_id, llm_call_id, sequence_number, provider_tool_call_id, tool_name,
     arguments_raw, arguments, result, status, started_at, completed_at)
SELECT turn_id, id, 0, 'call-1', 'valid', '{"player_id":1}', '{"player_id":1}'::jsonb, 'done', 'succeeded', now(), now() FROM call_row WHERE round_number = 0
UNION ALL
SELECT turn_id, id, 1, 'call-2', 'invalid', '{"player_id":', NULL, '', 'parse_error', now(), now() FROM call_row WHERE round_number = 0
UNION ALL
SELECT turn_id, id, 2, 'call-3', 'json-null', '{"value":"\u0000"}', NULL, '', 'parse_error', now(), now() FROM call_row WHERE round_number = 0
UNION ALL
SELECT turn_id, id, 0, 'call-1', 'second', '{"player_id":2}', '{"player_id":2}'::jsonb, 'done again', 'succeeded', now(), now() FROM call_row WHERE round_number = 1`,
		normalizedCallsJSON, repeatedProviderCallJSON)
}

func jsonColumn(t *testing.T, conn *pgx.Conn, query string) string {
	t.Helper()
	var value string
	require.NoError(t, conn.QueryRow(context.Background(), query).Scan(&value))
	return value
}

func TestIntegration_ToolCallNormalizationMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, toolCallNormalizationVersion-1)
	seedNormalizedToolCalls(t, conn)

	migrateTo(t, dbURL, toolCallNormalizationVersion)
	for _, table := range []string{"sim_agent_turn_messages", "maurice_messages"} {
		exists, _ := columnNullable(t, conn, table, "tool_calls")
		assert.False(t, exists)
	}
	assert.Equal(t, 4, countRows(t, conn, `SELECT count(*) FROM sim_agent_tool_calls WHERE round_index = 0 AND message_ordinal = 0`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM sim_agent_tool_calls WHERE round_index = 1 AND message_ordinal = 2`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM sim_agent_tool_calls WHERE tool_name = 'surrogate' AND provider_tool_call_id = 'call-4' AND message_ordinal = 0 AND tool_type = 'function'`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM sim_agent_turn_messages WHERE ordinal = 1 AND result_tool_round_index = 0 AND result_tool_sequence = 0`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM sim_agent_turn_messages WHERE ordinal = 3 AND result_tool_round_index = 1 AND result_tool_sequence = 0`))
	assert.Equal(t, 6, countRows(t, conn, `SELECT count(*) FROM maurice_tool_calls WHERE message_id IS NOT NULL`))
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM maurice_tool_calls WHERE llm_call_id IS NULL AND status IS NULL`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_tool_calls call_row JOIN maurice_llm_calls llm_call ON llm_call.id = call_row.llm_call_id JOIN maurice_messages message ON message.id = call_row.message_id WHERE llm_call.round_number = 1 AND message.message_number = 2`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_messages result_message JOIN maurice_tool_calls call_row ON call_row.id = result_message.result_tool_call_id JOIN maurice_messages call_message ON call_message.id = call_row.message_id WHERE result_message.message_number = 1 AND call_message.message_number = 0`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_messages result_message JOIN maurice_tool_calls call_row ON call_row.id = result_message.result_tool_call_id JOIN maurice_messages call_message ON call_message.id = call_row.message_id WHERE result_message.message_number = 3 AND call_message.message_number = 2`))
	assert.Equal(t, 3, countRows(t, conn, `SELECT count(*) FROM maurice_tool_calls WHERE arguments IS NULL`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_tool_calls WHERE tool_name = 'surrogate' AND arguments_raw = '{"value":"\ud800"}' AND arguments IS NULL`))

	migrateTo(t, dbURL, toolCallNormalizationVersion-1)
	assert.JSONEq(t, normalizedSimulationCallsJSON, jsonColumn(t, conn,
		`SELECT tool_calls::text FROM sim_agent_turn_messages WHERE ordinal = 0`))
	assert.JSONEq(t, repeatedProviderCallJSON, jsonColumn(t, conn,
		`SELECT tool_calls::text FROM sim_agent_turn_messages WHERE ordinal = 2`))
	assert.JSONEq(t, normalizedCallsJSON, jsonColumn(t, conn,
		`SELECT tool_calls::text FROM maurice_messages WHERE message_number = 0`))
	assert.JSONEq(t, repeatedProviderCallJSON, jsonColumn(t, conn,
		`SELECT tool_calls::text FROM maurice_messages WHERE message_number = 2`))
	assert.Equal(t, "call-1", jsonColumn(t, conn,
		`SELECT tool_call_id FROM maurice_messages WHERE message_number = 3`))
	assert.Equal(t, "call-1", jsonColumn(t, conn,
		`SELECT tool_call_id FROM sim_agent_turn_messages WHERE ordinal = 3`))

	migrateTo(t, dbURL, toolCallNormalizationVersion)
}

func TestIntegration_ToolCallNormalizationRejectsNonArrayCalls(t *testing.T) {
	tests := []struct {
		name    string
		update  string
		wantErr string
	}{
		{
			name:    "maurice",
			update:  `UPDATE maurice_messages SET tool_calls = '{"not":"a list"}'::jsonb WHERE message_number = 0`,
			wantErr: "maurice_messages.tool_calls contains a non-array value",
		},
		{
			name:    "simulation",
			update:  `UPDATE sim_agent_turn_messages SET tool_calls = '{"not":"a list"}'::jsonb WHERE ordinal = 0`,
			wantErr: "sim_agent_turn_messages.tool_calls contains a non-array value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, dbURL := freshDatabase(t)
			migrateTo(t, dbURL, toolCallNormalizationVersion-1)
			seedNormalizedToolCalls(t, conn)
			execSQL(t, conn, tt.update)

			m, err := openMigrator(migrationsFS, dbURL)
			require.NoError(t, err)
			defer func() { _, _ = m.Close() }()
			err = m.Migrate.Migrate(toolCallNormalizationVersion)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
