package simulation

import (
	"context"
	"math"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowIDForPool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		poolID int32
		want   string
	}{
		{1, "sim-pool-1"},
		{42, "sim-pool-42"},
		{100, "sim-pool-100"},
		{math.MaxInt32, "sim-pool-2147483647"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, WorkflowIDForPool(tc.poolID))
	}
}

// TestWorkflowIDForPoolMatchesGeneratedColumn checks the Go rule against
// the sim_pools.workflow_id generated column on real PostgreSQL, from the
// first serial id to the largest int4 one.
func TestWorkflowIDForPoolMatchesGeneratedColumn(t *testing.T) {
	t.Parallel()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	seed := simfixtures.NewSeeder(t, pool)
	serialID := seed.Pool("serial")
	explicitIDs := []int32{1_000_000, math.MaxInt32}
	for _, id := range explicitIDs {
		seed.Exec(`INSERT INTO sim_pools (id, name, season, num_teams, waiver_days, draft_rounds,
			max_llm_cost_usd_per_pool, categories)
			SELECT $1, 'explicit', season, num_teams, waiver_days, draft_rounds,
			max_llm_cost_usd_per_pool, categories FROM sim_pools WHERE id = $2`, id, serialID)
	}

	rows, err := pool.Query(context.Background(), `SELECT id, workflow_id FROM sim_pools ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var seen []int32
	for rows.Next() {
		var (
			id         int32
			workflowID string
		)
		require.NoError(t, rows.Scan(&id, &workflowID))
		assert.Equal(t, workflowID, WorkflowIDForPool(id), "pool %d", id)
		seen = append(seen, id)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, append([]int32{serialID}, explicitIDs...), seen)
}
