package simulation

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPg_SimTurnTranscriptLinksAndProjectsArguments(t *testing.T) {
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	seed := simfixtures.NewSeeder(t, pool)
	poolID := seed.Pool("transcript")
	agentID := seed.Agent(simfixtures.Agent{PoolID: poolID, TeamName: "Links", Model: "model"})
	date := time.Date(2024, 10, 4, 0, 0, 0, 0, time.UTC)
	turnID := seed.Turn(simfixtures.Turn{
		PoolID: poolID, AgentID: agentID, Phase: "daily", SimDate: &date,
		Status: "ok", Model: "model", CostUSD: "0", CompletedAt: time.Now(),
	})
	ctx := context.Background()
	q := sqlcdb.New(pool)
	_, err := q.InsertSimAgentTurnMessage(ctx, []sqlcdb.InsertSimAgentTurnMessageParams{
		{TurnID: turnID, Ordinal: 0, Role: "assistant"},
		{TurnID: turnID, Ordinal: 1, Role: "tool", Content: "done"},
		{TurnID: turnID, Ordinal: 2, Role: "assistant"},
		{TurnID: turnID, Ordinal: 3, Role: "tool", Content: "done again"},
	})
	require.NoError(t, err)

	rawArguments := []string{
		`{"player_id":1}`,
		`{"player_id":`,
		`{"value":"\u0000"}`,
		`{"value":"\\u0000"}`,
		`{"value":"\ud800"}`,
	}
	wantValid := []bool{true, false, false, true, false}
	for sequence, raw := range rawArguments {
		var valid bool
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT pg_input_is_valid($1::text, 'jsonb')`, raw).Scan(&valid))
		assert.Equal(t, wantValid[sequence], valid)
		require.NoError(t, q.InsertSimAgentToolCall(ctx, sqlcdb.InsertSimAgentToolCallParams{
			TurnID: turnID, RoundIndex: 0, Sequence: int32(sequence),
			MessageOrdinal:     pgtype.Int4{Int32: 0, Valid: true},
			ProviderToolCallID: pgtype.Text{String: "call-1", Valid: true},
			ToolType:           "function", ToolName: "tool", ArgumentsRaw: raw,
			Outcome: "accepted",
		}))
	}
	require.NoError(t, q.InsertSimAgentToolCall(ctx, sqlcdb.InsertSimAgentToolCallParams{
		TurnID: turnID, RoundIndex: 1, Sequence: 0,
		MessageOrdinal:     pgtype.Int4{Int32: 2, Valid: true},
		ProviderToolCallID: pgtype.Text{String: "call-2", Valid: true},
		ToolType:           "function", ToolName: "tool", ArgumentsRaw: `{}`,
		Outcome: "accepted",
	}))
	require.NoError(t, q.LinkSimAgentTurnMessageResults(ctx, sqlcdb.LinkSimAgentTurnMessageResultsParams{
		TurnID: turnID, Ordinals: []int32{1, 3}, RoundIndexes: []int32{0, 1}, Sequences: []int32{0, 0},
	}))

	messages, err := q.ListSimAgentTurnMessages(ctx, turnID)
	require.NoError(t, err)
	require.Len(t, messages, 4)
	assert.False(t, messages[0].ToolCallID.Valid)
	assert.Equal(t, "call-1", messages[1].ToolCallID.String)
	assert.False(t, messages[2].ToolCallID.Valid)
	assert.Equal(t, "call-2", messages[3].ToolCallID.String)
	assert.Equal(t, 3, simArgumentCount(t, pool, turnID, `arguments IS NOT NULL`))
	assert.Equal(t, 3, simArgumentCount(t, pool, turnID, `arguments IS NULL`))
}

func simArgumentCount(t *testing.T, pool *pgxpool.Pool, turnID int32, predicate string) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT count(*) FROM sim_agent_tool_calls WHERE turn_id = $1 AND "+predicate,
		turnID).Scan(&count))
	return count
}
