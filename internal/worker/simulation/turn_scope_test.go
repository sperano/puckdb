package simulation

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyScopeJSON is a turn's identity block as the inputs serialized it
// when they listed PoolID / AgentID / SimDate / PoolConfig / AgentConfig
// one by one: flat top-level keys, no nested scope object.
const legacyScopeJSON = `"pool_id": 3, "agent_id": 11, "sim_date": "2024-11-15",
	"pool_config": {"season": 20242025, "num_teams": 6, "waiver_days": 2},
	"agent_config": {"provider": "anthropic", "model": "claude-haiku-4-5", "strategy": "balanced"}`

func sampleTurnScope(t *testing.T) TurnScope {
	t.Helper()
	return TurnScope{
		PoolID:      3,
		AgentID:     11,
		SimDate:     pgDate(t, "2024-11-15"),
		PoolConfig:  PoolConfig{Season: 20242025, NumTeams: 6, WaiverDays: 2},
		AgentConfig: AgentConfig{Provider: "anthropic", Model: "claude-haiku-4-5", Strategy: "balanced"},
	}
}

// topLevelKeys returns the sorted top-level JSON keys of v.
func topLevelKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &obj))
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// The embedded TurnScope must flatten into the same top-level keys the
// inputs had before the embedding, so activities scheduled by an older
// worker (and pending in Temporal history) decode on a newer one and
// the reverse.
func TestTurnInputs_KeepLegacyTopLevelKeys(t *testing.T) {
	scope := sampleTurnScope(t)
	cases := map[string]struct {
		in   any
		want []string
	}{
		"DraftPickInput": {
			in: DraftPickInput{TurnScope: scope},
			want: []string{
				"agent_config", "agent_id", "available_ids", "draft_prompt", "pick",
				"pool_config", "pool_id", "ranked_goalies", "ranked_skaters", "round",
				"sim_date", "taken",
			},
		},
		"BuildManageRosterContextInput": {
			in: BuildManageRosterContextInput{TurnScope: scope},
			want: []string{
				"agent_config", "agent_id", "free_agents", "pool_config",
				"pool_id", "sim_date",
			},
		},
		"ManageRosterInput": {
			in: ManageRosterInput{TurnScope: scope},
			want: []string{
				"agent_config", "agent_id", "daily_prompt", "free_agents", "on_waivers",
				"pending_claims", "pool_config", "pool_id", "positions", "roster",
				"sim_date",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, topLevelKeys(t, tc.in))
		})
	}
}

func TestTurnInputs_DecodeLegacyFlatPayload(t *testing.T) {
	want := sampleTurnScope(t)

	var draft DraftPickInput
	require.NoError(t, json.Unmarshal([]byte(`{`+legacyScopeJSON+`, "round": 2, "pick": 4}`), &draft))
	assert.Equal(t, want, draft.TurnScope)
	assert.Equal(t, int32(2), draft.Round)
	assert.Equal(t, int32(4), draft.Pick)

	var build BuildManageRosterContextInput
	require.NoError(t, json.Unmarshal([]byte(`{`+legacyScopeJSON+`, "free_agents": [7]}`), &build))
	assert.Equal(t, want, build.TurnScope)
	assert.Equal(t, []int64{7}, build.FreeAgents)

	var manage ManageRosterInput
	require.NoError(t, json.Unmarshal([]byte(`{`+legacyScopeJSON+`, "on_waivers": [9]}`), &manage))
	assert.Equal(t, want, manage.TurnScope)
	assert.Equal(t, []int64{9}, manage.OnWaivers)
}

func TestSimStateTurnScope_ResolvesAgentConfig(t *testing.T) {
	cfg := PoolConfig{Season: 20242025, NumTeams: 2}
	state := simState{
		PoolID:     5,
		PoolConfig: cfg,
		AgentConfigByID: map[int32]AgentConfig{
			1: {Provider: "anthropic", Model: "a"},
			2: {Provider: "openai", Model: "b"},
		},
	}
	day := pgDate(t, "2024-10-09")

	got := state.turnScope(2, day)

	assert.Equal(t, TurnScope{
		PoolID:      5,
		AgentID:     2,
		SimDate:     day,
		PoolConfig:  cfg,
		AgentConfig: AgentConfig{Provider: "openai", Model: "b"},
	}, got)
}

// The draft pick carries the season start as its day, the same value
// the field-by-field construction set.
func TestAssembleDraftPickInput_ScopeIsSeasonStart(t *testing.T) {
	start := pgDate(t, "2024-10-08")
	agent := AgentConfig{Provider: "anthropic", Model: "m"}
	state := simState{
		PoolID:          4,
		PoolConfig:      PoolConfig{NumTeams: 2, DraftRounds: 1},
		AgentConfigByID: map[int32]AgentConfig{9: agent},
		SeasonStartDate: start,
	}

	in := assembleDraftPickInput(state, 9, 1, 2, LoadDraftCandidatesResult{}, nil, nil, "", nil)

	assert.Equal(t, TurnScope{PoolID: 4, AgentID: 9, SimDate: start, PoolConfig: state.PoolConfig, AgentConfig: agent}, in.TurnScope)
	assert.Equal(t, int32(1), in.Round)
	assert.Equal(t, int32(2), in.Pick)
}
