// Package simfixtures gives simulation tests a private, migrated PostgreSQL
// database and inserts the pool, agent, turn, tool-call, player and draft
// rows they need. Each NewDatabase call creates its own database next to the
// one PUCKDB_TEST_PG_URL names and drops it at cleanup, so these tests never
// truncate tables other packages share. Only tests import this package.
package simfixtures

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/stretchr/testify/require"
)

const (
	// EnvTestPGURL names the throwaway server (see CLAUDE.md, "Database-backed
	// tests"); tests skip without it.
	EnvTestPGURL = "PUCKDB_TEST_PG_URL"
	// databasePrefix keeps the "test" marker other harnesses look for.
	databasePrefix = "puckdb_test_sim"
	// setupTimeout bounds creating, migrating and dropping one database.
	setupTimeout = 2 * time.Minute

	// fixtureSeason is the season every fixture pool belongs to; Pool
	// inserts its seasons row on first use.
	fixtureSeason = 20242025
	// fixtureProvider is the provider of every fixture agent and turn.
	fixtureProvider = "anthropic"
)

// NewDatabase creates and migrates an empty database and returns its URL.
// It skips the test when PUCKDB_TEST_PG_URL is unset or the role cannot
// create databases.
func NewDatabase(t testing.TB) string {
	t.Helper()
	baseURL := os.Getenv(EnvTestPGURL)
	if baseURL == "" {
		t.Skipf("%s not set", EnvTestPGURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()

	name := databasePrefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := pgx.Connect(ctx, baseURL)
	require.NoError(t, err)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Skipf("cannot create a throwaway database (needs CREATEDB): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), setupTimeout)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(cleanupCtx)
	})

	parsed, err := url.Parse(baseURL)
	require.NoError(t, err)
	parsed.Path = "/" + name
	require.NoError(t, database.MigrateUp(parsed.String()))
	return parsed.String()
}

// NewPool opens a pool on dbURL that is closed at cleanup.
func NewPool(t testing.TB, dbURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// Seeder inserts fixture rows. It writes only the columns the simulation
// readers select, leaving the rest at their defaults.
type Seeder struct {
	t    testing.TB
	pool *pgxpool.Pool
}

// NewSeeder returns a Seeder writing through pool.
func NewSeeder(t testing.TB, pool *pgxpool.Pool) *Seeder {
	return &Seeder{t: t, pool: pool}
}

// Exec runs one statement, for bulk fixtures the typed helpers do not
// cover.
func (s *Seeder) Exec(sql string, args ...any) {
	s.t.Helper()
	_, err := s.pool.Exec(context.Background(), sql, args...)
	require.NoError(s.t, err)
}

func (s *Seeder) insertID(sql string, args ...any) int32 {
	s.t.Helper()
	var id int32
	require.NoError(s.t, s.pool.QueryRow(context.Background(), sql, args...).Scan(&id))
	return id
}

// Pool inserts a pool (and the fixture season it references) and returns
// its id.
func (s *Seeder) Pool(name string) int32 {
	s.t.Helper()
	s.Exec(`INSERT INTO seasons (id, standings_start, standings_end)
		VALUES ($1, DATE '2024-10-04', DATE '2025-04-17') ON CONFLICT (id) DO NOTHING`, fixtureSeason)
	return s.insertID(`INSERT INTO sim_pools (name, season, num_teams, waiver_days, draft_rounds,
		max_llm_cost_usd_per_pool, categories)
		VALUES ($1, $2, 2, 2, 2, 0, ARRAY['G'])
		RETURNING id`, name, fixtureSeason)
}

// Agent is one sim_agents row.
type Agent struct {
	PoolID          int32
	TeamName        string
	StrategySummary string
	Model           string
	DraftPosition   *int32
}

// Agent inserts an agent and returns its id.
func (s *Seeder) Agent(a Agent) int32 {
	s.t.Helper()
	return s.insertID(`INSERT INTO sim_agents (pool_id, provider, model, strategy, team_name,
		strategy_summary, draft_position)
		VALUES ($1, $2, $3, 'fixture strategy', $4, $5, $6)
		RETURNING id`, a.PoolID, fixtureProvider, a.Model, a.TeamName, a.StrategySummary, a.DraftPosition)
}

// Player inserts a players row. A nil position stores NULL.
func (s *Seeder) Player(id int64, first, last string, position *string) {
	s.t.Helper()
	s.Exec(`INSERT INTO players (id, first_name, last_name, position)
		VALUES ($1, $2, $3, $4::player_position)`, id, first, last, position)
}

// Turn is one sim_agent_turns row. SimDate must be nil exactly when Phase is
// "team_name"; PickNumber keeps several draft turns of one agent distinct.
type Turn struct {
	PoolID      int32
	AgentID     int32
	Phase       string
	SimDate     *time.Time
	PickNumber  int32
	Status      string
	ErrorKind   *string
	Model       string
	Rounds      int32
	CostUSD     string
	CompletedAt time.Time
}

// Turn inserts a turn and returns its id.
func (s *Seeder) Turn(tr Turn) int32 {
	s.t.Helper()
	return s.insertID(`INSERT INTO sim_agent_turns (pool_id, agent_id, sim_date, phase, pick_number,
		status, error_kind, provider, model, rounds, cost_usd, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::numeric, $12, $12)
		RETURNING id`,
		tr.PoolID, tr.AgentID, tr.SimDate, tr.Phase, tr.PickNumber,
		tr.Status, tr.ErrorKind, fixtureProvider, tr.Model, tr.Rounds, tr.CostUSD, tr.CompletedAt)
}

// DeleteTurn removes a turn (and its tool calls), leaving an id gap as a
// retried turn does.
func (s *Seeder) DeleteTurn(id int32) {
	s.t.Helper()
	s.Exec(`DELETE FROM sim_agent_turns WHERE id = $1`, id)
}

// ToolCall is one sim_agent_tool_calls row.
type ToolCall struct {
	TurnID        int32
	RoundIndex    int32
	Sequence      int32
	ToolName      string
	RecoveredName *string
	ArgumentsRaw  string
	Outcome       string
	FailureReason *string
	AppliedTxID   *int32
}

// ToolCall inserts a tool call.
func (s *Seeder) ToolCall(c ToolCall) {
	s.t.Helper()
	s.Exec(`INSERT INTO sim_agent_tool_calls (turn_id, round_index, sequence, tool_name,
		recovered_name, arguments_raw, result, outcome, failure_reason, applied_transaction_id)
		VALUES ($1, $2, $3, $4, $5, $6, 'fixture result', $7, $8, $9)`,
		c.TurnID, c.RoundIndex, c.Sequence, c.ToolName, c.RecoveredName,
		c.ArgumentsRaw, c.Outcome, c.FailureReason, c.AppliedTxID)
}

// DraftPick is one draft_pick sim_transactions row. A nil PlayerID stores
// NULL.
type DraftPick struct {
	PoolID    int32
	AgentID   int32
	PlayerID  *int64
	Round     int32
	Pick      int32
	Reasoning string
}

// DraftPick inserts a draft pick and returns its transaction id.
func (s *Seeder) DraftPick(p DraftPick) int32 {
	s.t.Helper()
	return s.insertID(`INSERT INTO sim_transactions (pool_id, agent_id, date, type, player_id,
		reasoning, round, pick)
		VALUES ($1, $2, DATE '2024-10-01', 'draft_pick', $3, $4, $5, $6)
		RETURNING id`, p.PoolID, p.AgentID, p.PlayerID, p.Reasoning, p.Round, p.Pick)
}
