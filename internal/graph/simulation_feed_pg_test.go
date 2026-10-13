package graph

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queryCounter is a pgx.QueryTracer counting the statements a pool runs.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// take returns the count since the last take.
func (c *queryCounter) take() int64 { return c.n.Swap(0) }

// feedFixture is a resolver over a fresh database whose queries are
// counted, and a seeder writing through a separate, uncounted pool.
type feedFixture struct {
	dbURL   string
	r       *Resolver
	queries *queryCounter
	seed    *simfixtures.Seeder
	pool    int32
	agent   int32
}

func newFeedFixture(t *testing.T) *feedFixture {
	t.Helper()
	dbURL := simfixtures.NewDatabase(t)
	cfg, err := pgxpool.ParseConfig(dbURL)
	require.NoError(t, err)
	counter := &queryCounter{}
	cfg.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(traced.Close)

	f := &feedFixture{dbURL: dbURL, r: &Resolver{Queries: sqlcdb.New(traced)}, queries: counter,
		seed: simfixtures.NewSeeder(t, simfixtures.NewPool(t, dbURL))}
	f.pool = f.seed.Pool("feed")
	f.agent = f.seed.Agent(simfixtures.Agent{PoolID: f.pool, TeamName: "Feeders", Model: "m"})
	return f
}

var feedStart = time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC)

// turn inserts a daily turn of agent in pool on day i.
func (f *feedFixture) turn(pool, agent int32, i int) int32 {
	day := feedStart.AddDate(0, 0, i)
	return f.seed.Turn(simfixtures.Turn{PoolID: pool, AgentID: agent, Phase: "daily", SimDate: &day,
		Status: "ok", Model: "m", CostUSD: "0.25", CompletedAt: day})
}

func (f *feedFixture) feed(t *testing.T, poolID, afterID *int, limit int) *model.SimTurnFeedPage {
	t.Helper()
	page, err := f.r.simTurnFeed(context.Background(), poolID, afterID, &limit)
	require.NoError(t, err)
	return page
}

func feedIDs(p *model.SimTurnFeedPage) []int {
	ids := make([]int, len(p.Turns))
	for i, tr := range p.Turns {
		ids[i] = tr.ID
	}
	return ids
}

func intIDs(ids []int32) []int {
	out := make([]int, len(ids))
	for i, id := range ids {
		out[i] = int(id)
	}
	return out
}

// The pool backlog is the pool's latest turns even when another pool's
// turns interleave and retries left id gaps; the old MAX(id)-10 watermark
// showed 4 of them here (ids 43, 45, 47 and 49).
func TestSimTurnFeed_LatestPoolTurnsWithInterleavingAndGaps(t *testing.T) {
	f := newFeedFixture(t)
	other := f.seed.Pool("other")
	otherAgent := f.seed.Agent(simfixtures.Agent{PoolID: other, TeamName: "Others", Model: "m"})
	var mine []int32
	for i := range 24 {
		mine = append(mine, f.turn(f.pool, f.agent, i))
		f.turn(other, otherAgent, i)
	}
	f.seed.DeleteTurn(mine[20]) // a retried turn: deleted, re-inserted later
	mine = append(mine[:20], mine[21:]...)
	mine = append(mine, f.turn(f.pool, f.agent, 30))

	pool := int(f.pool)
	page := f.feed(t, &pool, nil, simTurnFeedDefaultLimit)
	want := intIDs(mine[len(mine)-simTurnFeedDefaultLimit:])
	assert.Equal(t, want, feedIDs(page), "latest 10 of the pool, oldest first")
	assert.Equal(t, want[len(want)-1], page.LastID)
	assert.False(t, page.HasMore)
	assert.Equal(t, int64(2), f.queries.take(), "turns + tool calls; no ids, no name query")

	all := f.feed(t, nil, nil, simTurnFeedDefaultLimit)
	assert.Len(t, all.Turns, simTurnFeedDefaultLimit, "exactly 10 across pools despite the gap")
	assert.Equal(t, int(mine[len(mine)-1]), all.LastID)
}

func TestSimTurnFeed_PagesAfterID(t *testing.T) {
	f := newFeedFixture(t)
	var ids []int32
	for i := range 25 {
		ids = append(ids, f.turn(f.pool, f.agent, i))
	}
	const pageSize = 10
	after := 0
	var seen []int
	for range 3 {
		page := f.feed(t, nil, &after, pageSize)
		seen = append(seen, feedIDs(page)...)
		assert.Equal(t, len(seen) < len(ids), page.HasMore)
		after = page.LastID
	}
	assert.Equal(t, intIDs(ids), seen)
	f.queries.take()

	empty := f.feed(t, nil, &after, pageSize)
	assert.Empty(t, empty.Turns)
	assert.NotNil(t, empty.Turns)
	assert.Equal(t, after, empty.LastID, "an empty page echoes afterId")
	assert.False(t, empty.HasMore)
	assert.Equal(t, int64(1), f.queries.take(), "an empty poll is one query")

	exact := int(ids[len(ids)-pageSize-1])
	full := f.feed(t, nil, &exact, pageSize)
	assert.Len(t, full.Turns, pageSize)
	assert.False(t, full.HasMore, "a page that ends at the newest turn has no more")
}

// A turn's tool calls come in execution order up to the cap, with the
// full count; turns without calls have an empty list.
func TestSimTurnFeed_ToolCallCapAndCount(t *testing.T) {
	f := newFeedFixture(t)
	busy, quiet, idle := f.turn(f.pool, f.agent, 0), f.turn(f.pool, f.agent, 1), f.turn(f.pool, f.agent, 2)
	const busyCalls = simTurnFeedMaxToolCallsPerTurn + 3
	// Inserted in reverse so the order comes from the query, not the heap.
	for i := busyCalls - 1; i >= 0; i-- {
		f.seed.ToolCall(simfixtures.ToolCall{TurnID: busy, RoundIndex: int32(i / 4), Sequence: int32(i % 4),
			ToolName: fmt.Sprintf("call%02d", i), ArgumentsRaw: "{}", Outcome: "accepted"})
	}
	for i := range 2 {
		f.seed.ToolCall(simfixtures.ToolCall{TurnID: quiet, Sequence: int32(i), ToolName: "q", ArgumentsRaw: "{}", Outcome: "accepted"})
	}

	page := f.feed(t, nil, nil, simTurnFeedDefaultLimit)
	require.Equal(t, intIDs([]int32{busy, quiet, idle}), feedIDs(page))
	b := page.Turns[0]
	assert.Equal(t, busyCalls, b.ToolCallCount)
	require.Len(t, b.ToolCalls, simTurnFeedMaxToolCallsPerTurn)
	for i, c := range b.ToolCalls {
		assert.Equal(t, fmt.Sprintf("call%02d", i), c.ToolName)
		assert.Equal(t, [2]int{i / 4, i % 4}, [2]int{c.RoundIndex, c.Sequence})
	}
	assert.Equal(t, 2, page.Turns[1].ToolCallCount)
	assert.Len(t, page.Turns[1].ToolCalls, 2)
	assert.Equal(t, 0, page.Turns[2].ToolCallCount)
	assert.NotNil(t, page.Turns[2].ToolCalls)
	assert.Empty(t, page.Turns[2].ToolCalls)
	assert.Equal(t, int64(2), f.queries.take())
}

// Player names: first appearance, deduplicated, unknown and non-integer
// ids left out, a blank first name trimmed, a blank name dropped; the
// whole page costs three queries.
func TestSimTurnFeed_PlayerNamesAndQueryCount(t *testing.T) {
	f := newFeedFixture(t)
	f.seed.Player(8478402, "Connor", "McDavid", nil)
	f.seed.Player(8471214, "", "Ovechkin", nil)
	f.seed.Player(8000001, " ", " ", nil)
	calls := map[string][]string{
		`{"player_id":8478402,"drop_player_id":8471214}`:                                 {"Ovechkin", "Connor McDavid"},
		`{"slots":[{"player_id":8471214},{"player_id":99999999},{"player_id":8471214}]}`: {"Ovechkin"},
		`{"player_id":8478402.7}`:                                                        {},
		`{"player_id":"8478402"}`:                                                        {},
		`{"player_id":8000001}`:                                                          {},
		`{"player_id":8478402} bad`:                                                      {},
	}
	var turns []int32
	for args := range calls {
		turn := f.turn(f.pool, f.agent, len(turns))
		turns = append(turns, turn)
		f.seed.ToolCall(simfixtures.ToolCall{TurnID: turn, ToolName: "t", ArgumentsRaw: args, Outcome: "accepted"})
	}
	f.queries.take()

	page := f.feed(t, nil, nil, simTurnFeedDefaultLimit)
	assert.Equal(t, int64(3), f.queries.take(), "turns, tool calls, names")
	require.Len(t, page.Turns, len(calls))
	for _, tr := range page.Turns {
		require.Len(t, tr.ToolCalls, 1)
		c := tr.ToolCalls[0]
		assert.Equal(t, calls[c.Arguments], c.PlayerNames, c.Arguments)
	}
}

// arguments_raw is collapsed and cut at the byte bound without splitting a
// rune.
func TestSimTurnFeed_ArgumentsCutOnRuneBoundary(t *testing.T) {
	f := newFeedFixture(t)
	turn := f.turn(f.pool, f.agent, 0)
	raw := "{\n  \"text\": \"x" + strings.Repeat("é", SimTurnFeedMaxArgumentsBytes) + "\"\n}"
	f.seed.ToolCall(simfixtures.ToolCall{TurnID: turn, ToolName: "note", ArgumentsRaw: raw, Outcome: "accepted"})

	page := f.feed(t, nil, nil, simTurnFeedDefaultLimit)
	got := page.Turns[0].ToolCalls[0].Arguments
	assert.True(t, utf8.ValidString(got))
	assert.LessOrEqual(t, len(got), SimTurnFeedMaxArgumentsBytes)
	assert.Greater(t, len(got), SimTurnFeedMaxArgumentsBytes-utf8.UTFMax)
	assert.True(t, strings.HasPrefix(strings.Join(strings.Fields(raw), " "), got))
}

// legacyDraftSQL is the inline query `sim draft` ran against PostgreSQL
// before simDraftPicks; ListSimDraftPicks must return the same rows.
const legacyDraftSQL = `
	SELECT
	  a.id, a.team_name, a.strategy_summary, a.model, a.draft_position,
	  t.round, t.pick, COALESCE(t.player_id, 0),
	  COALESCE(NULLIF(TRIM(BOTH FROM p.first_name || ' ' || p.last_name), ''), '') AS player_name,
	  COALESCE(p.position::text, '?') AS position,
	  t.reasoning
	FROM sim_transactions t
	JOIN sim_agents a ON a.id = t.agent_id
	LEFT JOIN players p ON p.id = t.player_id
	WHERE t.pool_id = $1
	  AND t.type = 'draft_pick'
	ORDER BY a.draft_position NULLS LAST, a.id, t.round, t.pick`

func legacyDraftPicks(t *testing.T, dbURL string, poolID int32) []*model.SimDraftPick {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dbURL)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	rows, err := conn.Query(context.Background(), legacyDraftSQL, poolID)
	require.NoError(t, err)
	defer rows.Close()
	out := []*model.SimDraftPick{}
	for rows.Next() {
		var p model.SimDraftPick
		var agentID, round, pick int32
		var draftPos *int32
		require.NoError(t, rows.Scan(&agentID, &p.TeamName, &p.StrategySummary, &p.Model, &draftPos,
			&round, &pick, &p.PlayerID, &p.PlayerName, &p.Position, &p.Reasoning))
		p.AgentID, p.Round, p.Pick = int(agentID), int(round), int(pick)
		if draftPos != nil {
			v := int(*draftPos)
			p.DraftPosition = &v
		}
		p.PlayerName = strings.TrimSpace(p.PlayerName)
		out = append(out, &p)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestSimDraftPicks_MatchLegacyQuery(t *testing.T) {
	f := newFeedFixture(t)
	r, seed, counter := f.r, f.seed, f.queries

	center := "C"
	seed.Player(1, "Connor", "McDavid", &center)
	seed.Player(2, "", "Ovechkin", &center)
	seed.Player(3, "No", "Position", nil)
	pool, empty := seed.Pool("draft"), seed.Pool("empty")
	first, second := int32(1), int32(2)
	a := seed.Agent(simfixtures.Agent{PoolID: pool, TeamName: "A", StrategySummary: "s", Model: "m", DraftPosition: &second})
	b := seed.Agent(simfixtures.Agent{PoolID: pool, Model: "m", DraftPosition: &first})
	c := seed.Agent(simfixtures.Agent{PoolID: pool, TeamName: "Unordered"})
	ids := func(v int64) *int64 { return &v }
	for _, p := range []simfixtures.DraftPick{
		{PoolID: pool, AgentID: a, PlayerID: ids(1), Round: 2, Pick: 3, Reasoning: "second round"},
		{PoolID: pool, AgentID: a, PlayerID: ids(2), Round: 1, Pick: 2},
		{PoolID: pool, AgentID: b, PlayerID: ids(3), Round: 1, Pick: 1, Reasoning: "no position"},
		{PoolID: pool, AgentID: c, Round: 1, Pick: 3, Reasoning: "no player"},
	} {
		seed.DraftPick(p)
	}
	seed.Exec(`INSERT INTO sim_transactions (pool_id, agent_id, date, type, reasoning)
		VALUES ($1, $2, DATE '2024-10-02', 'pass', 'not a pick')`, pool, a)

	got, err := r.simDraftPicks(context.Background(), int(pool))
	require.NoError(t, err)
	assert.Equal(t, int64(2), counter.take(), "pool check + picks")
	want := legacyDraftPicks(t, f.dbURL, pool)
	require.Len(t, want, 4)
	assert.Equal(t, want, got)
	assert.Equal(t, []int{int(b), int(a), int(a), int(c)}, []int{got[0].AgentID, got[1].AgentID, got[2].AgentID, got[3].AgentID})

	none, err := r.simDraftPicks(context.Background(), int(empty))
	require.NoError(t, err)
	assert.NotNil(t, none, "an existing pool without picks is an empty list, not null")
	assert.Empty(t, none)

	missing, err := r.simDraftPicks(context.Background(), int(empty)+1)
	require.NoError(t, err)
	assert.Nil(t, missing, "a missing pool is null")
}

// More picks than the bound is an error, not a silently shortened list.
func TestSimDraftPicks_RowBound(t *testing.T) {
	f := newFeedFixture(t)
	f.seed.Exec(`INSERT INTO sim_transactions (pool_id, agent_id, date, type, round, pick)
		SELECT $1, $2, DATE '2024-10-01', 'draft_pick', n, 1 FROM generate_series(1, $3::int) AS n`,
		f.pool, f.agent, simDraftPicksMaxRows+1)
	_, err := f.r.simDraftPicks(context.Background(), int(f.pool))
	require.EqualError(t, err, fmt.Sprintf("pool %d has more than %d draft picks", f.pool, simDraftPicksMaxRows))

	f.seed.Exec(`DELETE FROM sim_transactions WHERE pool_id = $1 AND round = 1`, f.pool)
	picks, err := f.r.simDraftPicks(context.Background(), int(f.pool))
	require.NoError(t, err)
	assert.Len(t, picks, simDraftPicksMaxRows)
}
