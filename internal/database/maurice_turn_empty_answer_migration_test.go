package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// mauriceTurnEmptyAnswerVersion adds 'empty_answer' to maurice_turns.error_class.
const mauriceTurnEmptyAnswerVersion = 37

// Up accepts a failed 'empty_answer' turn; down turns it into an
// 'internal_error' turn, still failed, and refuses the class again; up runs
// again.
func TestIntegration_MauriceTurnEmptyAnswerMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, mauriceTurnEmptyAnswerVersion-1)
	execSQL(t, conn, `
WITH user_row AS (
    INSERT INTO app_users (instance, auth_provider, auth_subject, username)
    VALUES ('puckdb', 'authentik', 'empty-answer', 'empty-answer') RETURNING id
)
INSERT INTO maurice_conversations (user_id) SELECT id FROM user_row`)
	insertFailedTurn := `
INSERT INTO maurice_turns (conversation_id, user_id, turn_number, idempotency_key, request_hash,
    status, started_at, completed_at, error_class)
SELECT id, user_id, $1, $2, decode('00', 'hex'), 'failed', now(), now(), $3 FROM maurice_conversations`
	requireRejected(t, conn, "empty_answer before the migration", checkViolation("maurice_turns_error_class_check"),
		insertFailedTurn, 0, "before", "empty_answer")

	migrateTo(t, dbURL, mauriceTurnEmptyAnswerVersion)
	execSQL(t, conn, insertFailedTurn, 0, "empty", "empty_answer")
	execSQL(t, conn, insertFailedTurn, 1, "timeout", "timeout")

	migrateTo(t, dbURL, mauriceTurnEmptyAnswerVersion-1)
	assert.Equal(t, 1, countRows(t, conn,
		`SELECT count(*) FROM maurice_turns WHERE idempotency_key = 'empty' AND status = 'failed' AND error_class = 'internal_error'`),
		"down keeps the empty-answer turn as a failed internal error")
	assert.Equal(t, 1, countRows(t, conn,
		`SELECT count(*) FROM maurice_turns WHERE idempotency_key = 'timeout' AND error_class = 'timeout'`),
		"down leaves the other classes alone")
	requireRejected(t, conn, "empty_answer after down", checkViolation("maurice_turns_error_class_check"),
		`UPDATE maurice_turns SET error_class = 'empty_answer' WHERE idempotency_key = 'empty'`)

	migrateTo(t, dbURL, mauriceTurnEmptyAnswerVersion)
	execSQL(t, conn, `UPDATE maurice_turns SET error_class = 'empty_answer' WHERE idempotency_key = 'empty'`)
}
