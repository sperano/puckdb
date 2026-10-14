package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/spf13/cobra"
)

// ============================================================================
// `puckdb sim tail` — stream the simulation turn telemetry to stdout.
//
// Reads the API's simTurnFeed query (sim_agent_turns plus their
// sim_agent_tool_calls) and prints each turn followed by its tool calls
// (indented). Default behavior is tail -f-style: print the most recent N
// turns for context, then poll every poll interval for new turns and print
// them as they commit.
//
// Tailing is driven by a monotonic watermark on `sim_agent_turns.id`
// (SERIAL). Caveat: a SERIAL id is assigned at INSERT time but only
// visible at COMMIT time, so a turn whose transaction commits after a
// higher id has been printed is never shown. That is not rare: every
// agent of a pool picks its team name concurrently, and a tail without
// --pool sees every running pool's turns. A gap-free tail would need a
// look-back window or logical replication; the tail accepts the gap.
// ============================================================================

// tailPollInterval is the cadence between feed requests in follow mode.
// Turns happen on the order of seconds (LLM latency dominates), so 1s
// gives near-real-time updates without spamming the API.
const tailPollInterval = 1 * time.Second

// tailBacklog is how many recent turns to print on startup before
// entering follow mode. Gives the user context without dumping the
// entire history of a long-running pool.
const tailBacklog = 10

// tailArgPreviewMax is the maximum number of characters of a tool
// call's argument blob to print before truncating with an ellipsis.
// Keeps lines readable while preserving the gist of small calls. The
// server's preview (graph.SimTurnFeedMaxArgumentsBytes) is longer.
const tailArgPreviewMax = 80

// simClientFlagGroups lists the flag groups every `sim` subcommand
// exposes: they all talk to the API over GraphQL. Defined once and
// shared by InitFlags and BindFlags so the two can never drift.
var simClientFlagGroups = []*config.FlagGroup{
	&config.APIBasicAuthFlags,
}

func cmdSimTail() *cobra.Command {
	var poolID int
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Tail agent turn telemetry (turns + tool calls) to stdout",
		Long: `Stream sim_agent_turns and their sim_agent_tool_calls to stdout
as new turns commit, through the API server's GraphQL endpoint. Prints
the last ` + fmt.Sprintf("%d", tailBacklog) + ` turns for context, then follows. Ctrl-C to exit.

  puckdb sim tail               # all pools
  puckdb sim tail --pool 3      # one pool

Each turn is one summary line plus one indented line per tool call.`,
		// No positional pool ID: `sim tail 3` would otherwise tail every pool.
		Args:    cobra.NoArgs,
		PreRunE: bindFlagsPreRunE(simClientFlagGroups...),
		RunE: func(cmd *cobra.Command, args []string) error {
			if poolID < 0 {
				return fmt.Errorf("--pool must be a pool ID, or 0 for all pools; got %d", poolID)
			}
			client, err := getGraphQLClient()
			if err != nil {
				return err
			}
			return runSimTail(cmd.Context(), client, cmd.OutOrStdout(), poolID, tailPollInterval)
		},
	}
	cmd.Flags().IntVar(&poolID, "pool", 0, "Restrict to a single pool ID (0 = all pools)")
	config.InitFlags(cmd.Flags(), simClientFlagGroups...)
	return cmd
}

// tailEntry is one turn of a feed page with the tool calls the server
// returned for it.
type tailEntry struct {
	Turn  tailTurn
	Calls []tailToolCall
}

// tailPage is one simTurnFeed response.
type tailPage struct {
	Entries []tailEntry
	// LastID is the watermark to pass back as afterID.
	LastID int
	// HasMore asks for the next page at once.
	HasMore bool
}

// simTurnFeeder reads the turn feed; *GraphQLClient implements it. A nil
// poolID means every pool, a nil afterID the latest limit turns.
type simTurnFeeder interface {
	SimTurnFeed(ctx context.Context, poolID, afterID *int, limit int) (tailPage, error)
}

// runSimTail is the long-running loop. Split from cmdSimTail so tests
// can drive it with a fake feeder. Returns nil on graceful ctx cancel
// (Ctrl-C), even mid-request; returns any other feed error.
func runSimTail(ctx context.Context, feeder simTurnFeeder, w io.Writer, poolID int, interval time.Duration) error {
	var pool *int
	if poolID > 0 {
		pool = &poolID
	}
	// The backlog prints at once, without waiting `interval`.
	page, err := feeder.SimTurnFeed(ctx, pool, nil, tailBacklog)
	if err != nil {
		return tailError(ctx, err)
	}
	writeTailPage(w, page)
	lastID := page.LastID

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if lastID, err = followSimTail(ctx, feeder, w, pool, lastID); err != nil {
			return tailError(ctx, err)
		}
	}
}

// followSimTail prints every turn after lastID, a page at a time, and
// returns the new watermark. A steady-state poll is one empty page.
func followSimTail(ctx context.Context, feeder simTurnFeeder, w io.Writer, pool *int, lastID int) (int, error) {
	for {
		page, err := feeder.SimTurnFeed(ctx, pool, &lastID, graph.SimTurnFeedMaxLimit)
		if err != nil {
			return lastID, err
		}
		writeTailPage(w, page)
		lastID = page.LastID
		if !page.HasMore {
			return lastID, nil
		}
	}
}

// tailError hides the error a Ctrl-C causes in an in-flight request.
func tailError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("sim turn feed: %w", err)
}

func writeTailPage(w io.Writer, page tailPage) {
	for _, e := range page.Entries {
		fmt.Fprint(w, formatTailRow(e.Turn, e.Calls))
	}
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
	// ToolCallCount is every tool call of the turn, including any the
	// server left out of the page.
	ToolCallCount int
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
	// Arguments is the server's whitespace-collapsed preview of
	// arguments_raw.
	Arguments     string
	Outcome       string
	FailureReason *string
	AppliedTxID   *int64
	// PlayerNames are the names of the player ids in the arguments.
	PlayerNames []string
}

// formatTailRow renders one turn (header) and its tool calls (indented)
// to a string. Pure function — no DB, no I/O — so the test layer can
// pin the exact layout. The trailing newline is included. When the
// server left calls out (ToolCallCount above len(calls)), one more
// indented line says how many.
//
// Example output:
//
//	[14:32:01] pool=3 sonnet  daily  2024-11-15  ok  4r  $0.018
//	           r0/0  drop_player {"player_id":8478402}  accepted  tx=10421  [Connor McDavid]
//	           r1/0  set_lineup  {...}                  validation_rejected  reason=invalid_slot
func formatTailRow(t tailTurn, calls []tailToolCall) string {
	var b strings.Builder
	b.WriteString(formatTurnHeader(t))
	b.WriteByte('\n')
	for _, c := range calls {
		b.WriteString(formatToolCallLine(c))
		b.WriteByte('\n')
	}
	if omitted := t.ToolCallCount - len(calls); omitted > 0 {
		fmt.Fprintf(&b, "%s… %d more tool calls\n", tailCallIndent, omitted)
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

// tailCallIndent is 11 spaces: it lines up with the column after
// "[HH:MM:SS] " so tool-call lines visually nest under their turn header.
const tailCallIndent = "           "

func formatToolCallLine(c tailToolCall) string {
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
	if len(c.PlayerNames) > 0 {
		suffix += "  [" + strings.Join(c.PlayerNames, ", ") + "]"
	}
	return fmt.Sprintf("%sr%d/%d  %-18s %s  %s%s",
		tailCallIndent, c.RoundIndex, c.Sequence, name,
		truncateArgs(c.Arguments, tailArgPreviewMax),
		c.Outcome, suffix,
	)
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
