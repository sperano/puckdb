//go:build livellm

package simulation

// TestLiveSmallSeasonOllama — the operator-run live-model smoke test.
// NEVER runs in CI. Same seeded-scenario approach as the hermetic
// layer, but the REAL AgentFactory/NewAgent path drives 4 agents
// against an actual Ollama host. This exercises what the mocks can't:
// the OpenAI-compatible client against Ollama, tool-call parsing from
// a small model, timeout handling, and the validation feedback loop
// with genuinely erratic output.
//
// Run it (from ws/puckdb or a worktree):
//
//	PUCKDB_TEST_PG_URL=postgres://puckdb:foo@localhost:15432/puckdb_integration_test?sslmode=disable \
//	PUCKDB_TEST_REDIS_ADDR=localhost:16379 \
//	PUCKDB_TEST_OLLAMA_URL=http://ollama.local:11434 \
//	go test -tags=livellm -count=1 -timeout 45m -v ./worker/simulation/ -run TestLiveSmallSeasonOllama
//
// Extra gates on top of the shared suite env:
//
//	PUCKDB_TEST_OLLAMA_URL    Ollama base URL (no /v1 suffix). Unset → skip.
//	PUCKDB_TEST_OLLAMA_MODEL  Model to run (default llama3.1:8b). The
//	                          test pings {URL}/api/tags first and skips
//	                          with a clear message if the host is
//	                          unreachable or the model isn't pulled.
//
// Assertions are LIVENESS, not quality: the pool completes, every
// (agent, day) produced a daily turn marker, no turn errored, and the
// tool-call outcome distribution is logged. Do NOT assert on specific
// rosters or decisions — small-model output is nondeterministic.
//
// Network note (2026-07-07): pfSense blocks the cluster subnet
// 192.0.2.10/24 from initiating into the LAN, so CLUSTER-side sims
// need a pass rule for 192.0.2.10:11434/tcp. Runs from a LAN
// machine (like this test) are unaffected.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	defaultOllamaModel = "llama3.1:8b"

	liveSeason  int32 = 20282029
	liveStart         = "2026-06-01"
	liveDays          = 7
	liveOffset  int64 = 2000
	liveRounds        = 4 // 1C+1D+1G+1BN — tiny roster, few LLM calls
	liveTimeout       = 30 * time.Minute
)

// requireOllama checks the live-LLM gates: env var set, host
// reachable, model pulled. Returns the base URL. Skips otherwise.
func requireOllama(t *testing.T) (baseURL, model string) {
	t.Helper()
	baseURL = os.Getenv("PUCKDB_TEST_OLLAMA_URL")
	if baseURL == "" {
		t.Skip("set PUCKDB_TEST_OLLAMA_URL to run the live Ollama smoke (e.g. http://ollama.local:11434)")
	}
	model = os.Getenv("PUCKDB_TEST_OLLAMA_MODEL")
	if model == "" {
		model = defaultOllamaModel
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("Ollama at %s unreachable: %v", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Ollama at %s returned %d from /api/tags", baseURL, resp.StatusCode)
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		t.Skipf("Ollama /api/tags decode failed: %v", err)
	}
	for _, m := range tags.Models {
		if m.Name == model {
			return baseURL, model
		}
	}
	names := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	t.Skipf("model %q not pulled on %s (available: %v) — set PUCKDB_TEST_OLLAMA_MODEL or `ollama pull %s`",
		model, baseURL, names, model)
	return "", ""
}

func TestLiveSmallSeasonOllama(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ollamaURL, model := requireOllama(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()

	ctx := context.Background()
	q := sqlcdb.New(pgPool)

	// Same synthetic-scenario builder as the hermetic layer — the live
	// model drafts from and scores against these seeded players.
	cfg := smallSeasonConfig{
		Season:         liveSeason,
		StartDate:      liveStart,
		Days:           liveDays,
		GamesPerDay:    2,
		NumTeams:       4,
		SkatersPerTeam: 10,
		GoaliesPerTeam: 2,
		Seed:           20280601,
		IDOffset:       liveOffset,
	}
	// The returned line data is unused here — liveness only, no
	// attribution recompute (agent decisions are nondeterministic).
	const numAgents = 4
	seedSmallSeason(t, ctx, pgPool, cfg, numAgents*liveRounds)

	capUSD, err := pgNumericFromFloat(0) // Ollama models cost $0; cap disabled
	require.NoError(t, err)
	poolRow, err := q.InsertSimPool(ctx, sqlcdb.InsertSimPoolParams{
		Name:                 "livellm_ollama_smoke",
		Season:               liveSeason,
		Status:               string(PoolStatusDraft),
		NumTeams:             numAgents,
		WaiverDays:           2,
		DraftRounds:          liveRounds,
		MaxLLMCostUsdPerPool: capUSD,
		Categories:           []string{"G", "A", "+/-", "PIM", "PPP", "SOG", "W", "GA", "GAA"},
		RosterC:              1,
		RosterD:              1,
		RosterG:              1,
		RosterBN:             1,
		StopAfter:            StopAfterNever.String(),
		// Window start from the pool config (migration 000016); the
		// day count still comes from MaxSeasonDays so the two
		// mechanisms compose (Layer A exercises start+end instead).
		StartDate:     mustPgDate(liveStart),
		MaxSeasonDays: liveDays,
	})
	require.NoError(t, err)
	poolID := poolRow.ID

	strategies := []string{
		"Build a balanced roster across all categories.",
		"Stack high-shot-volume forwards; chase G, A, SOG.",
		"Get the best goaltender available; protect W and GAA.",
		"Chase upside: prefer players with the best recent stats.",
	}
	agentIDs := make([]int32, numAgents)
	for i := 0; i < numAgents; i++ {
		a, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
			PoolID:   poolID,
			Provider: "ollama",
			Model:    model,
			Strategy: strategies[i],
			// Small models are slow — generous per-call HTTP timeout
			// (the LLM activity's StartToClose is 5 min).
			TimeoutSeconds: 240,
		})
		require.NoError(t, err)
		agentIDs[i] = a.ID
	}
	for i, id := range agentIDs {
		seedWaiverPriority(t, ctx, pgPool, poolID, id, int32(i+1))
	}

	// REAL agent path: no factory override; NewAgent builds an
	// OpenAI-compatible client pointed at the Ollama host.
	providers := llm.NewProviderConfigs(llm.ProviderConfigsInput{
		OllamaBaseURL: ollamaURL + "/v1",
	})
	stop := setupLiveIntegrationWorker(t, tc, pgPool, redisClient, providers)
	defer stop()

	runWorkflowToCompletion(t, ctx, tc, poolID, liveTimeout)

	defer func() {
		if t.Failed() {
			dumpTurnTelemetry(t, ctx, pgPool, poolID)
		}
	}()

	// ------------------------------------------------------------------
	// Liveness assertions.
	// ------------------------------------------------------------------
	assertPoolCompleted(t, ctx, pgPool, poolID)

	// Every (agent, day) produced a completed daily turn.
	var markers int
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sim_transactions WHERE pool_id=$1 AND type='daily_turn_done'`,
		poolID).Scan(&markers))
	assert.Equal(t, numAgents*liveDays, markers, "one daily_turn_done marker per agent per day")

	// No errored turns — validation rejections are fine (that's the
	// feedback loop working), turn-level errors are not.
	var errored int
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sim_agent_turns WHERE pool_id=$1 AND status='errored'`,
		poolID).Scan(&errored))
	assert.Zero(t, errored, "no turn should end errored (see dumpTurnTelemetry output)")

	// Log the tool-call outcome distribution — the human-facing signal
	// for "how well did this model drive the tool surface".
	rows, err := pgPool.Query(ctx, `
		SELECT tc.tool_name, tc.outcome, COUNT(*)
		FROM sim_agent_tool_calls tc
		JOIN sim_agent_turns tn ON tn.id = tc.turn_id
		WHERE tn.pool_id=$1
		GROUP BY tc.tool_name, tc.outcome
		ORDER BY tc.tool_name, tc.outcome
	`, poolID)
	require.NoError(t, err)
	defer rows.Close()
	t.Logf("tool-call outcome distribution (model=%s):", model)
	for rows.Next() {
		var tool, outcome string
		var n int
		require.NoError(t, rows.Scan(&tool, &outcome, &n))
		t.Logf("  %-16s %-22s %d", tool, outcome, n)
	}
	require.NoError(t, rows.Err())

	// Every agent committed a team name (Phase 0 would have failed the
	// workflow otherwise — this is a readability check, not a gate).
	for _, id := range agentIDs {
		var name string
		require.NoError(t, pgPool.QueryRow(ctx,
			`SELECT team_name FROM sim_agents WHERE id=$1`, id).Scan(&name))
		assert.NotEmptyf(t, name, "agent %d team name", id)
		t.Logf("agent %d team: %s", id, name)
	}
}
