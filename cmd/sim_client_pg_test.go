package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// simTailDraftGolden is what the direct-PostgreSQL `sim tail` and `sim
// draft` printed for the seeded scenario, captured with TZ=UTC before they
// moved to GraphQL. One section differs on purpose: the old pool backlog
// showed the pool's turns among the last tailBacklog ids of every pool (6
// here); it now shows the pool's own latest turns (all 9).
const simTailDraftGolden = "testdata/sim_tail_draft.golden"

// goldenClock is the UTC "[HH:MM:SS]" that opens each golden turn header.
var goldenClock = regexp.MustCompile(`(?m)^\[(\d\d:\d\d:\d\d)\]`)

// localizeGoldenClock rewrites the golden UTC clock times in the local zone
// formatTurnHeader prints. Every scenario turn completes on scenarioStart's
// date, so that date's offset is exact.
func localizeGoldenClock(t *testing.T, golden []byte) []byte {
	t.Helper()
	return goldenClock.ReplaceAllFunc(golden, func(m []byte) []byte {
		clock, err := time.Parse(time.TimeOnly, string(m[1:len(m)-1]))
		require.NoError(t, err)
		y, mo, d := scenarioStart.Date()
		utc := time.Date(y, mo, d, clock.Hour(), clock.Minute(), clock.Second(), 0, time.UTC)
		return []byte("[" + utc.Local().Format(time.TimeOnly) + "]")
	})
}

// countingFeeder counts feed requests.
type countingFeeder struct {
	simTurnFeeder
	calls int
}

func (f *countingFeeder) SimTurnFeed(ctx context.Context, poolID, afterID *int, limit int) (tailPage, error) {
	f.calls++
	return f.simTurnFeeder.SimTurnFeed(ctx, poolID, afterID, limit)
}

// newSimGraphQLClient serves the real GraphQL handler over a fresh, seeded
// database and returns a client for it.
func newSimGraphQLClient(t *testing.T) (*GraphQLClient, *simfixtures.Seeder, simScenario) {
	t.Helper()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	seeder := simfixtures.NewSeeder(t, pool)
	sc := seedSimScenario(seeder)
	srv := httptest.NewServer(graphqlHandler(&graph.Resolver{Queries: sqlcdb.New(pool)}, graph.AdminAuth{}))
	t.Cleanup(srv.Close)
	return newClientForTestServer(srv), seeder, sc
}

// The CLI over GraphQL prints, byte for byte, what the direct-database
// commands printed: backlog, a follow that needs two pages, and the draft.
func TestSimTailAndDraftOverGraphQLMatchLegacyOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, seeder, sc := newSimGraphQLClient(t)
	var out bytes.Buffer

	out.WriteString("== backlog all pools\n")
	backlog, err := client.SimTurnFeed(ctx, nil, nil, tailBacklog)
	require.NoError(t, err)
	writeTailPage(&out, backlog)

	poolA := int(sc.poolA)
	fmt.Fprintf(&out, "== backlog of pool %d (all of its 9 turns)\n", poolA)
	poolBacklog, err := client.SimTurnFeed(ctx, &poolA, nil, tailBacklog)
	require.NoError(t, err)
	writeTailPage(&out, poolBacklog)

	sc.seedFollow(seeder)
	out.WriteString("== follow all pools\n")
	feeder := &countingFeeder{simTurnFeeder: client}
	last, err := followSimTail(ctx, feeder, &out, nil, backlog.LastID)
	require.NoError(t, err)
	assert.Equal(t, 2, feeder.calls, "%d follow turns take two pages", sc.followTurns)
	assert.Greater(t, last, backlog.LastID)

	out.WriteString("== draft\n")
	require.NoError(t, renderSimDraft(ctx, client, &out, poolA, false))
	out.WriteString("== draft --reasoning\n")
	require.NoError(t, renderSimDraft(ctx, client, &out, poolA, true))
	out.WriteString("== draft of a pool without picks\n")
	require.NoError(t, renderSimDraft(ctx, client, &out, int(sc.poolB), false))

	golden, err := os.ReadFile(simTailDraftGolden)
	require.NoError(t, err)
	assert.Equal(t, string(localizeGoldenClock(t, golden)), out.String())
}

func TestSimDraftOverGraphQLMissingPool(t *testing.T) {
	t.Parallel()
	client, _, sc := newSimGraphQLClient(t)
	missing := int(sc.poolB) + 1
	err := renderSimDraft(context.Background(), client, &bytes.Buffer{}, missing, false)
	require.EqualError(t, err, fmt.Sprintf("pool %d not found", missing))
}

// The server's argument checks reach the CLI as errors.
func TestSimTurnFeedOverGraphQLRejectsOutOfRangeArguments(t *testing.T) {
	t.Parallel()
	client, _, _ := newSimGraphQLClient(t)
	zero, negative := 0, -1
	_, err := client.SimTurnFeed(context.Background(), &zero, nil, tailBacklog)
	require.ErrorContains(t, err, "poolId must be between 1 and")
	_, err = client.SimTurnFeed(context.Background(), nil, &negative, tailBacklog)
	require.ErrorContains(t, err, "afterId must be between 0 and")
	_, err = client.SimTurnFeed(context.Background(), nil, nil, graph.SimTurnFeedMaxLimit+1)
	require.ErrorContains(t, err, "limit must be between 1 and")
	assert.False(t, strings.Contains(err.Error(), "predates"))
}

// simScenario is a seeded database for the sim tail/draft end-to-end tests:
// two pools whose turns interleave, players with blank first names and no
// position, tool calls that cover every tool-call line variant, and a draft
// with a pick whose player is NULL.
type simScenario struct {
	poolA, poolB int32
	// llama is pool B's agent; the follow turns are its.
	llama int32
	// followTurns is how many turns seedFollow adds; more than one feed page.
	followTurns int
}

const (
	scenarioMcDavid    int64 = 8478402
	scenarioDraisaitl  int64 = 8477934
	scenarioOvechkin   int64 = 8471214
	scenarioNoPosition int64 = 8480001
	scenarioUnknownID  int64 = 99999999
	// scenarioBacklogTurns is more than tailBacklog, so the backlog is cut.
	scenarioBacklogTurns = 14
	// scenarioFollowTurns is more than one simTurnFeed page.
	scenarioFollowTurns = 105
)

var scenarioStart = time.Date(2024, 11, 15, 14, 0, 0, 0, time.UTC)

func seedSimScenario(s *simfixtures.Seeder) simScenario {
	center, noPosition := "C", (*string)(nil)
	s.Player(scenarioMcDavid, "Connor", "McDavid", &center)
	s.Player(scenarioDraisaitl, "Leon", "Draisaitl", &center)
	s.Player(scenarioOvechkin, "", "Ovechkin", &center)
	s.Player(scenarioNoPosition, "Pos", "Less", noPosition)

	sc := simScenario{poolA: s.Pool("A"), poolB: s.Pool("B"), followTurns: scenarioFollowTurns}
	first, second := int32(1), int32(2)
	sonnet := s.Agent(simfixtures.Agent{PoolID: sc.poolA, TeamName: "Sonnet Stars", StrategySummary: "balanced", Model: "claude-sonnet", DraftPosition: &second})
	unnamed := s.Agent(simfixtures.Agent{PoolID: sc.poolA, Model: "claude-haiku", DraftPosition: &first})
	llama := s.Agent(simfixtures.Agent{PoolID: sc.poolB, TeamName: "Llamas", Model: "llama3.1:8b"})
	unordered := s.Agent(simfixtures.Agent{PoolID: sc.poolA, TeamName: "Late", StrategySummary: "punt G"})

	pickA := s.DraftPick(simfixtures.DraftPick{PoolID: sc.poolA, AgentID: sonnet, PlayerID: &scenarioMcDavidVar, Round: 1, Pick: 2,
		Reasoning: "Generational talent; " + strings.Repeat("unmatched production ", 5)})
	s.DraftPick(simfixtures.DraftPick{PoolID: sc.poolA, AgentID: unnamed, PlayerID: &scenarioOvechkinVar, Round: 1, Pick: 1, Reasoning: "Goals."})
	s.DraftPick(simfixtures.DraftPick{PoolID: sc.poolA, AgentID: unnamed, PlayerID: &scenarioNoPositionVar, Round: 2, Pick: 4})
	s.DraftPick(simfixtures.DraftPick{PoolID: sc.poolA, AgentID: sonnet, Round: 2, Pick: 3, Reasoning: "no player"})
	s.DraftPick(simfixtures.DraftPick{PoolID: sc.poolA, AgentID: unordered, PlayerID: &scenarioDraisaitlVar, Round: 1, Pick: 9, Reasoning: "late"})

	agents := []struct {
		pool, agent int32
		model       string
	}{{sc.poolA, sonnet, "claude-sonnet"}, {sc.poolB, llama, "llama3.1:8b"}, {sc.poolA, unnamed, "claude-haiku"}}
	for i := range scenarioBacklogTurns {
		a := agents[i%len(agents)]
		seedScenarioTurn(s, a.pool, a.agent, a.model, i, pickA)
	}
	sc.llama = llama
	return sc
}

// seedFollow adds followTurns plain daily turns of pool B after the
// backlog. They complete on scenarioStart's date, like every other turn, so
// the golden clock times convert to any local zone without a DST change.
func (sc simScenario) seedFollow(s *simfixtures.Seeder) {
	for i := range sc.followTurns {
		day := scenarioStart.AddDate(0, 1, i)
		s.Turn(simfixtures.Turn{PoolID: sc.poolB, AgentID: sc.llama, Phase: "daily", SimDate: &day,
			Status: "skipped", Model: "m", CostUSD: "0", CompletedAt: scenarioStart.Add(time.Hour + time.Duration(i)*time.Second)})
	}
}

var (
	scenarioMcDavidVar    = scenarioMcDavid
	scenarioDraisaitlVar  = scenarioDraisaitl
	scenarioOvechkinVar   = scenarioOvechkin
	scenarioNoPositionVar = scenarioNoPosition
)

// seedScenarioTurn inserts the i-th backlog turn; i picks the variant.
func seedScenarioTurn(s *simfixtures.Seeder, pool, agent int32, model string, i int, txID int32) {
	day := scenarioStart.AddDate(0, 0, i)
	completed := scenarioStart.Add(time.Duration(i) * time.Minute)
	turn := simfixtures.Turn{PoolID: pool, AgentID: agent, Phase: "daily", SimDate: &day, Status: "ok",
		Model: model, Rounds: int32(i%4 + 1), CostUSD: fmt.Sprintf("0.0%d75", i%10), CompletedAt: completed}
	switch i % 5 {
	case 0:
		turn.Phase, turn.SimDate = "team_name", nil
	case 3:
		kind := "provider_error"
		turn.Status, turn.ErrorKind = "errored", &kind
	}
	id := s.Turn(turn)
	if i%5 == 3 {
		return
	}
	recovered, reason := "add_player", "roster_full"
	calls := []simfixtures.ToolCall{
		{ToolName: "add_player", ArgumentsRaw: fmt.Sprintf(`{"player_id":%d,"drop_player_id":%d}`, scenarioMcDavid, scenarioDraisaitl),
			Outcome: "accepted", AppliedTxID: &txID},
		{ToolName: "set_lineup", ArgumentsRaw: fmt.Sprintf("{\n  \"slots\": [{\"player_id\": %d, \"slot\": \"C\"},\n {\"player_id\": %d, \"slot\": \"LW\"}, {\"player_id\": %d}]\n}",
			scenarioOvechkin, scenarioUnknownID, scenarioOvechkin), Outcome: "accepted"},
		{ToolName: "addPlayer", RecoveredName: &recovered, ArgumentsRaw: `{"player_id":"8478402"}`,
			Outcome: "validation_rejected", FailureReason: &reason},
		{ToolName: "note", ArgumentsRaw: `{"text":"x` + strings.Repeat("é", 60) + `"}`, Outcome: "accepted"},
		{ToolName: "broken", ArgumentsRaw: `{"player_id":`, Outcome: "parse_error"},
	}
	for seq, c := range calls[:i%len(calls)+1] {
		c.TurnID, c.RoundIndex, c.Sequence = id, int32(seq/2), int32(seq%2)
		s.ToolCall(c)
	}
}
