package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/cobra"
)

// ============================================================================
// `puckdb sim tail` — stream the simulation telemetry tables to stdout.
//
// Reads `sim_agent_turns` (one row per LLM-driven turn) and prints each
// turn followed by its `sim_agent_tool_calls` rows (indented). Default
// behavior is tail -f-style: print the most recent N turns for context,
// then poll every poll interval for new turns and print them as they
// commit.
//
// Tailing is driven by a monotonic watermark on `sim_agent_turns.id`
// (SERIAL). Caveat: a SERIAL id is assigned at INSERT time but only
// visible at COMMIT time, so two interleaved transactions could in
// principle make rows appear in id-reverse order. For the simulator
// this is exceedingly unlikely (turns commit atomically, one per agent
// at a time) — a bulletproof tail would need logical replication or a
// snapshot-based approach. We accept the simplification.
// ============================================================================

// tailPollInterval is the cadence between SELECTs in follow mode. Turns
// happen on the order of seconds (LLM latency dominates), so 1s gives
// near-real-time updates without spamming the DB.
const tailPollInterval = 1 * time.Second

// tailBacklog is how many recent turns to print on startup before
// entering follow mode. Gives the user context without dumping the
// entire history of a long-running pool.
const tailBacklog = 10

// tailArgPreviewMax is the maximum number of characters of a tool
// call's argument blob to print before truncating with an ellipsis.
// Keeps lines readable while preserving the gist of small calls.
const tailArgPreviewMax = 80

// simTailFlagGroups lists every flag group `sim tail` exposes. Defined
// once and shared by InitFlags and BindFlags so the two can never drift.
var simTailFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
}

func cmdSimTail() *cobra.Command {
	var poolID int
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Tail agent turn telemetry (turns + tool calls) to stdout",
		Long: `Stream sim_agent_turns and their sim_agent_tool_calls to stdout
as new turns commit. Prints the last ` + fmt.Sprintf("%d", tailBacklog) + ` turns for
context, then follows. Ctrl-C to exit.

  puckdb sim tail               # all pools
  puckdb sim tail --pool 3      # one pool

Each turn is one summary line plus one indented line per tool call.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), simTailFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			pool, err := openPGXPool(ctx)
			if err != nil {
				return fmt.Errorf("open db pool: %w", err)
			}
			defer pool.Close()
			return runSimTail(ctx, pool, cmd.OutOrStdout(), poolID, tailPollInterval)
		},
	}
	cmd.Flags().IntVar(&poolID, "pool", 0, "Restrict to a single pool ID (0 = all pools)")
	config.InitFlags(cmd.Flags(), simTailFlagGroups...)
	return cmd
}

// runSimTail is the long-running loop. Split from cmdSimTail so tests
// can drive it with a fake pool. Returns nil on graceful ctx cancel
// (Ctrl-C); returns an error only on unrecoverable DB errors.
func runSimTail(ctx context.Context, pool *pgxpool.Pool, w io.Writer, poolID int, interval time.Duration) error {
	// Pre-load player names so each tool-call line can decorate raw
	// player_id integers with a "[Connor McDavid]" suffix without an
	// extra round trip per line. The players table is small (~9.6k rows)
	// and grows slowly, so loading once at startup is fine — newly
	// added players just print without a name decoration until you
	// restart the tail.
	playerNames, err := loadPlayerNames(ctx, pool)
	if err != nil {
		return fmt.Errorf("load player names: %w", err)
	}

	// Pick the starting watermark: max(id) - tailBacklog. This shows the
	// most recent tailBacklog rows before we transition to "follow new
	// rows only" mode. Without the subtraction we'd dump the entire
	// table on the first tick of a long-running pool.
	startID, err := initialTailWatermark(ctx, pool, poolID)
	if err != nil {
		return fmt.Errorf("initial watermark: %w", err)
	}
	lastSeenID := startID

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// First pass runs immediately so the user sees the backlog without
	// waiting `interval` for the first tick.
	for {
		newLastSeen, err := drainSimTail(ctx, pool, w, poolID, lastSeenID, playerNames)
		if err != nil {
			return err
		}
		lastSeenID = newLastSeen

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// loadPlayerNames returns a "player ID → display name" map covering
// every row in the players table. Used to decorate raw player_id
// integers in tool-call args so a human reading the tail sees
// "[Connor McDavid]" alongside the ID.
func loadPlayerNames(ctx context.Context, pool *pgxpool.Pool) (map[int64]string, error) {
	rows, err := pool.Query(ctx, `SELECT id, first_name, last_name FROM players`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]string, 16384)
	for rows.Next() {
		var id int64
		var first, last string
		if err := rows.Scan(&id, &first, &last); err != nil {
			return nil, err
		}
		out[id] = strings.TrimSpace(first + " " + last)
	}
	return out, rows.Err()
}

// initialTailWatermark returns max(id) - tailBacklog (clamped to 0), so
// drainSimTail will pick up the most recent tailBacklog rows on its
// first pass. Returns 0 on an empty table so we follow from the
// beginning.
func initialTailWatermark(ctx context.Context, pool *pgxpool.Pool, poolID int) (int64, error) {
	sql := `SELECT COALESCE(MAX(id), 0) FROM sim_agent_turns`
	args := []any{}
	if poolID > 0 {
		sql += ` WHERE pool_id = $1`
		args = append(args, poolID)
	}
	var maxID int64
	if err := pool.QueryRow(ctx, sql, args...).Scan(&maxID); err != nil {
		return 0, err
	}
	return max(maxID-int64(tailBacklog), 0), nil
}

// tailTurn is one sim_agent_turns row joined with the agent name. Kept
// as a struct (rather than a wide function signature) so formatTailRow
// stays purely about layout.
type tailTurn struct {
	ID          int64
	PoolID      int
	AgentID     int32
	TeamName    string
	Summary     string
	Model       string
	Phase       string
	SimDate     *time.Time
	Status      string
	ErrorKind   *string
	Rounds      int
	CostUSD     float64
	CompletedAt time.Time
}

// agentLabel composes the operator-visible identity for one agent
// from its raw fields. Precedence:
//   - team_name set: "<team_name> (<summary>, <model>)" — summary
//     omitted when empty, model omitted when empty.
//   - team_name empty (pre-PickTeamName or after a failed pick):
//     "agent #<id>" so the row still has a stable identifier.
//
// One function, multiple call sites — tail, standings, future
// dashboards. Keep it pure (no DB, no I/O) so tests don't need a stub.
func agentLabel(agentID int32, teamName, summary, model string) string {
	if teamName == "" {
		return fmt.Sprintf("agent #%d", agentID)
	}
	parts := make([]string, 0, 2)
	if summary != "" {
		parts = append(parts, summary)
	}
	if model != "" {
		parts = append(parts, model)
	}
	if len(parts) == 0 {
		return teamName
	}
	return fmt.Sprintf("%s (%s)", teamName, strings.Join(parts, ", "))
}

// tailToolCall is one sim_agent_tool_calls row, restricted to the
// fields we print.
type tailToolCall struct {
	RoundIndex    int
	Sequence      int
	ToolName      string
	RecoveredName *string
	ArgumentsRaw  string
	Outcome       string
	FailureReason *string
	AppliedTxID   *int64
}

// drainSimTail SELECTs all turns with id > lastSeenID, prints each one
// followed by its tool calls, and returns the new high-water-mark id.
// On a steady-state poll this returns lastSeenID unchanged with no rows
// fetched.
func drainSimTail(ctx context.Context, pool *pgxpool.Pool, w io.Writer, poolID int, lastSeenID int64, playerNames map[int64]string) (int64, error) {
	turns, err := selectNewTurns(ctx, pool, poolID, lastSeenID)
	if err != nil {
		return lastSeenID, fmt.Errorf("select turns: %w", err)
	}
	for _, t := range turns {
		calls, err := selectToolCallsForTurn(ctx, pool, t.ID)
		if err != nil {
			return lastSeenID, fmt.Errorf("select tool calls for turn %d: %w", t.ID, err)
		}
		fmt.Fprint(w, formatTailRow(t, calls, playerNames))
		if t.ID > lastSeenID {
			lastSeenID = t.ID
		}
	}
	return lastSeenID, nil
}

func selectNewTurns(ctx context.Context, pool *pgxpool.Pool, poolID int, lastSeenID int64) ([]tailTurn, error) {
	// Order by id ASC so we print in insertion order. Joining sim_agents
	// keeps the rendered line independent of an extra lookup table.
	sql := `
		SELECT t.id, t.pool_id, a.id, a.team_name, a.strategy_summary, a.model,
		       t.phase, t.sim_date, t.status,
		       t.error_kind, t.rounds, t.cost_usd, t.completed_at
		FROM sim_agent_turns t
		JOIN sim_agents a ON a.id = t.agent_id
		WHERE t.id > $1`
	args := []any{lastSeenID}
	if poolID > 0 {
		sql += ` AND t.pool_id = $2`
		args = append(args, poolID)
	}
	sql += ` ORDER BY t.id ASC`

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []tailTurn
	for rows.Next() {
		var t tailTurn
		if err := rows.Scan(&t.ID, &t.PoolID, &t.AgentID, &t.TeamName, &t.Summary, &t.Model,
			&t.Phase, &t.SimDate, &t.Status,
			&t.ErrorKind, &t.Rounds, &t.CostUSD, &t.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func selectToolCallsForTurn(ctx context.Context, pool *pgxpool.Pool, turnID int64) ([]tailToolCall, error) {
	const sql = `
		SELECT round_index, sequence, tool_name, recovered_name,
		       arguments_raw, outcome, failure_reason, applied_transaction_id
		FROM sim_agent_tool_calls
		WHERE turn_id = $1
		ORDER BY round_index, sequence`
	rows, err := pool.Query(ctx, sql, turnID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var out []tailToolCall
	for rows.Next() {
		var c tailToolCall
		if err := rows.Scan(&c.RoundIndex, &c.Sequence, &c.ToolName, &c.RecoveredName,
			&c.ArgumentsRaw, &c.Outcome, &c.FailureReason, &c.AppliedTxID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// formatTailRow renders one turn (header) and its tool calls (indented)
// to a string. Pure function — no DB, no I/O — so the test layer can
// pin the exact layout. The trailing newline is included.
//
// playerNames may be nil; in that case no [name] decoration is appended
// to tool-call lines.
//
// Example output:
//
//	[14:32:01] pool=3 sonnet  daily  2024-11-15  ok  4r  $0.018
//	           r0/0  drop_player {"player_id":8478402}  accepted  tx=10421  [Connor McDavid]
//	           r1/0  set_lineup  {...}                  validation_rejected  reason=invalid_slot
func formatTailRow(t tailTurn, calls []tailToolCall, playerNames map[int64]string) string {
	var b strings.Builder
	b.WriteString(formatTurnHeader(t))
	b.WriteByte('\n')
	for _, c := range calls {
		b.WriteString(formatToolCallLine(c, playerNames))
		b.WriteByte('\n')
	}
	return b.String()
}

func formatTurnHeader(t tailTurn) string {
	simDate := "          " // 10-wide placeholder for "YYYY-MM-DD"
	if t.SimDate != nil {
		simDate = t.SimDate.Format("2006-01-02")
	}
	// Error rows append err=<kind> so a failure is obvious at a glance
	// without scrolling right to find the absence of tool calls.
	errSuffix := ""
	if t.Status != "ok" && t.ErrorKind != nil && *t.ErrorKind != "" {
		errSuffix = fmt.Sprintf("  err=%s", *t.ErrorKind)
	}
	return fmt.Sprintf("[%s] pool=%d %-48s %-9s %s  %-7s  %dr  $%.3f%s",
		t.CompletedAt.Local().Format("15:04:05"),
		t.PoolID,
		agentLabel(t.AgentID, t.TeamName, t.Summary, t.Model),
		t.Phase,
		simDate,
		t.Status,
		t.Rounds,
		t.CostUSD,
		errSuffix,
	)
}

func formatToolCallLine(c tailToolCall, playerNames map[int64]string) string {
	// 11 spaces of indent lines up with the column after "[HH:MM:SS] "
	// so tool-call lines visually nest under their turn header.
	const indent = "           "
	name := c.ToolName
	if c.RecoveredName != nil && *c.RecoveredName != "" && *c.RecoveredName != c.ToolName {
		name = fmt.Sprintf("%s→%s", c.ToolName, *c.RecoveredName)
	}
	suffix := ""
	if c.AppliedTxID != nil {
		suffix = fmt.Sprintf("  tx=%d", *c.AppliedTxID)
	}
	if c.FailureReason != nil && *c.FailureReason != "" {
		suffix += fmt.Sprintf("  reason=%s", *c.FailureReason)
	}
	if p := playerNamesFromArgs(c.ArgumentsRaw, playerNames); p != "" {
		suffix += "  " + p
	}
	return fmt.Sprintf("%sr%d/%d  %-18s %s  %s%s",
		indent, c.RoundIndex, c.Sequence, name,
		truncateArgs(c.ArgumentsRaw, tailArgPreviewMax),
		c.Outcome, suffix,
	)
}

// playerNamesFromArgs walks the tool-call args JSON for player_id and
// drop_player_id integer values, resolves them through the lookup map,
// and returns a "[Name1, Name2]" string. Returns "" when the args have
// no resolvable player IDs (either the map is nil, the JSON is malformed,
// or no player_id key is present). Order matches first appearance in a
// stable JSON walk so set_lineup's slot ordering is preserved.
func playerNamesFromArgs(argsRaw string, playerNames map[int64]string) string {
	if len(playerNames) == 0 || argsRaw == "" {
		return ""
	}
	ids := extractPlayerIDs(argsRaw)
	if len(ids) == 0 {
		return ""
	}
	names := make([]string, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if n, ok := playerNames[id]; ok && n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// extractPlayerIDs walks JSON looking for any "player_id" or
// "drop_player_id" key whose value is a JSON number, and returns the
// values in first-appearance order. Returns nil on malformed JSON —
// the caller already prints the raw args, so silently skipping
// decoration is the right failure mode.
//
// Walks recursively so set_lineup's nested {"slots":[{"player_id":...}]}
// is covered without a per-tool special case.
func extractPlayerIDs(argsRaw string) []int64 {
	var root any
	if err := json.Unmarshal([]byte(argsRaw), &root); err != nil {
		return nil
	}
	var ids []int64
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			// Iterate keys in a stable order so output is deterministic
			// across Go map iteration. Go ≥1.22 randomizes map order,
			// which would otherwise flake the test layer.
			for _, k := range sortedKeys(n) {
				child := n[k]
				if k == "player_id" || k == "drop_player_id" {
					if id, ok := numberToInt64(child); ok {
						ids = append(ids, id)
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(root)
	return ids
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Stable order so the player-name decoration is deterministic
	// across Go map iteration (which is randomized as of 1.22).
	slices.Sort(keys)
	return keys
}

// numberToInt64 extracts an int64 from json-decoded value types. Default
// encoding/json puts numbers in float64; player IDs (~8-digit ints) fit
// in float64's 53-bit mantissa without loss, so the cast is safe.
func numberToInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	}
	return 0, false
}

// truncateArgs collapses whitespace and clamps to maxLen. JSONB blobs
// frequently contain embedded newlines that would break the one-line-
// per-call format; collapse first, truncate second.
func truncateArgs(s string, maxLen int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
