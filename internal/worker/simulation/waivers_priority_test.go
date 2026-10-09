package simulation

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agentsWithIDs(ids ...int32) []sqlcdb.SimAgent {
	out := make([]sqlcdb.SimAgent, len(ids))
	for i, id := range ids {
		out[i] = sqlcdb.SimAgent{ID: id, PoolID: 1}
	}
	return out
}

func TestCheckWaiverPriorityComplete(t *testing.T) {
	tests := []struct {
		name       string
		agents     []sqlcdb.SimAgent
		priorities []sqlcdb.SimWaiverPriority
		wantErr    string
	}{
		{
			name:       "every agent ranked 1..N",
			agents:     agentsWithIDs(10, 20, 30),
			priorities: []sqlcdb.SimWaiverPriority{priority(20, 1), priority(30, 2), priority(10, 3)},
		},
		{
			name: "no agents, no rows",
		},
		{
			name:    "no rows (draft order never recorded)",
			agents:  agentsWithIDs(10, 20),
			wantErr: "2 agents but 0 priority rows",
		},
		{
			name:       "agent missing a row",
			agents:     agentsWithIDs(10, 20, 30),
			priorities: []sqlcdb.SimWaiverPriority{priority(10, 1), priority(20, 2)},
			wantErr:    "3 agents but 2 priority rows",
		},
		{
			name:       "row for an agent outside the pool",
			agents:     agentsWithIDs(10, 20),
			priorities: []sqlcdb.SimWaiverPriority{priority(10, 1), priority(99, 2)},
			wantErr:    "agent 99 has a priority row but is not in the pool",
		},
		{
			name:       "agent ranked twice",
			agents:     agentsWithIDs(10, 20),
			priorities: []sqlcdb.SimWaiverPriority{priority(10, 1), priority(10, 2)},
			wantErr:    "agent 10 has more than one priority row",
		},
		{
			name:       "priority number repeated",
			agents:     agentsWithIDs(10, 20),
			priorities: []sqlcdb.SimWaiverPriority{priority(10, 1), priority(20, 1)},
			wantErr:    "priority 1 is held by more than one agent",
		},
		{
			name:       "priority zero",
			agents:     agentsWithIDs(10, 20),
			priorities: []sqlcdb.SimWaiverPriority{priority(10, 0), priority(20, 1)},
			wantErr:    "priority 0 of agent 10 is outside 1..2",
		},
		{
			name:       "priority beyond N (a gap)",
			agents:     agentsWithIDs(10, 20),
			priorities: []sqlcdb.SimWaiverPriority{priority(10, 1), priority(20, 3)},
			wantErr:    "priority 3 of agent 20 is outside 1..2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkWaiverPriorityComplete(tt.agents, tt.priorities)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, errWaiverPriorityIncomplete)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadWaiverPriority_ReportsInitialization(t *testing.T) {
	q := &stubSimQueries{
		initPriorityInserted:   2,
		listPriorityRows:       []sqlcdb.SimWaiverPriority{priority(1, 1), priority(2, 2)},
		listAgentsByPoolReturn: agentsWithIDs(1, 2),
	}
	got, initialized, err := loadWaiverPriority(context.Background(), q, 1)
	require.NoError(t, err)
	assert.True(t, initialized)
	assert.Equal(t, q.listPriorityRows, got)
	assert.Equal(t, []int32{1}, q.initPriorityCalls)

	q.initPriorityInserted = 0
	_, initialized, err = loadWaiverPriority(context.Background(), q, 1)
	require.NoError(t, err)
	assert.False(t, initialized, "a pool that already has rows is not re-initialized")
}

// SIM-I8: a pool whose priority rows do not rank every agent must not
// resolve claims (a claimant without a row would silently lose).
func (s *ProcessWaiversTestSuite) TestIncompletePriority_AbortsBeforeResolving() {
	t := s.T()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{claim(100, 1, 8478402, 0), claim(101, 2, 8478402, 0)}
	s.setPriorities(priority(1, 1), priority(2, 2))
	s.queries.listPriorityRows = []sqlcdb.SimWaiverPriority{priority(1, 1)} // agent 2 unranked

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.Error(t, err)
	assert.Contains(t, err.Error(), errWaiverPriorityIncomplete.Error())
	assert.Empty(t, s.queries.listClaimsForDuePlayersArgs, "claims are not read for an incomplete pool")
	assert.Empty(t, s.queries.resolveClaimCalls)
	assert.Empty(t, s.queries.insertRosterCalls)
	assert.Empty(t, s.queries.updatePriorityCalls)
}

func (s *ProcessWaiversTestSuite) TestPriorityInitialized_Reported() {
	t := s.T()
	s.setPriorities(priority(1, 1), priority(2, 2))
	s.queries.initPriorityInserted = 2

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.PriorityInitialized)
	assert.True(t, got.Skipped)
}
