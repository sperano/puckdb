package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// mauriceTurnToolNamesVersion adds maurice_turns.tool_names.
const mauriceTurnToolNamesVersion = 35

// Up gives an existing turn an empty list and refuses a non-array; down drops
// the column; up runs again.
func TestIntegration_MauriceTurnToolNamesMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, mauriceTurnToolNamesVersion-1)
	execSQL(t, conn, `
WITH user_row AS (
    INSERT INTO app_users (instance, auth_provider, auth_subject, username)
    VALUES ('puckdb', 'authentik', 'tool-names', 'tool-names') RETURNING id
), conversation AS (
    INSERT INTO maurice_conversations (user_id) SELECT id FROM user_row RETURNING id, user_id
)
INSERT INTO maurice_turns (conversation_id, user_id, turn_number, idempotency_key, request_hash, status, started_at)
SELECT id, user_id, 0, 'tool-names', decode('00', 'hex'), 'running', now() FROM conversation`)

	migrateTo(t, dbURL, mauriceTurnToolNamesVersion)
	exists, nullable := columnNullable(t, conn, "maurice_turns", "tool_names")
	assert.True(t, exists)
	assert.False(t, nullable)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_turns WHERE tool_names = '[]'::jsonb`),
		"an existing turn starts with no tools")
	for _, value := range []string{`{}`, `"get_player"`, `null`} {
		requireRejected(t, conn, "a non-array tool list "+value, checkViolation("maurice_turns_tool_names_array"),
			`UPDATE maurice_turns SET tool_names = $1::jsonb`, value)
	}
	execSQL(t, conn, `UPDATE maurice_turns SET tool_names = '["get_player"]'::jsonb`)

	migrateTo(t, dbURL, mauriceTurnToolNamesVersion-1)
	exists, _ = columnNullable(t, conn, "maurice_turns", "tool_names")
	assert.False(t, exists)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_turns`), "down keeps the turns")

	migrateTo(t, dbURL, mauriceTurnToolNamesVersion)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM maurice_turns WHERE tool_names = '[]'::jsonb`))
}
