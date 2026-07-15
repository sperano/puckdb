package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"

	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

// ============================================================================
// `puckdb sim` — simulation pool CLI.
//
// Per PLAN.md > "CLI Commands" — V1 scope is bootstrap + observe:
// create, advance, run (= auto_advance), pause, cancel, status.
// `list`, `destroy`, `agent-log` are out of V1 scope (use the
// GraphQL playground until friction warrants them).
//
// All commands talk to the API server via the existing GraphQL
// client — same pattern as `puckdb db init` and friends. The CLI
// itself is thin: argument parsing, YAML-config reading, output
// formatting. The actual behavior lives in the resolvers (Phase
// 4.2) and the workflow (Phase 3.2).
// ============================================================================

func cmdSim() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sim",
		Short: "Hockey pool simulator",
		Long:  `Create and drive the puckdb simulation. Operates against the API server's GraphQL endpoint.`,
	}
	cmd.AddCommand(
		cmdSimCreate(),
		cmdSimCancel(),
		cmdSimStatus(),
		cmdSimTail(),
		cmdSimDraft(),
	)
	return cmd
}

// ----------------------------------------------------------------------------
// create
// ----------------------------------------------------------------------------

func cmdSimCreate() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new sim pool",
		Long: `Create a new simulation pool from a YAML config file. The
config file is the only input — there are no inline flags for
agents, categories, etc., because pool config is immutable after
creation and lining up an inline-flag schema with the config schema
isn't worth the friction.

Example config:

  name: Crapettes Sonnet vs Haiku vs Llama
  season: 20242025
  categories: [G, A, PIM, PPP, SOG, W, GA]
  rosterPositions:
    - {slot: C,  count: 2}
    - {slot: LW, count: 2}
    - {slot: RW, count: 2}
    - {slot: D,  count: 3}
    - {slot: G,  count: 2}
    - {slot: BN, count: 5}
  waiverDays: 2
  draftRounds: 18
  maxLlmCostUsdPerPool: 200.0
  agents:
    - name: Sonnet
      provider: anthropic
      model: claude-sonnet-4-7
      strategy: |
        Build a balanced roster across all categories. Avoid one-category
        specialists. Prefer steady veterans over volatile rookies, and keep
        the bench shallow so waiver pickups can rotate in quickly.
    - name: Haiku
      provider: anthropic
      model: claude-haiku-4-5
      strategy: |
        Punt GAA and SV%. Stack high-shot-volume forwards to dominate G,
        A, SOG, and PPP. Stream goalies aggressively for W only — drop
        them between starts.
    - name: Llama
      provider: ollama
      model: "llama3.1:8b"
      apiBase: "http://localhost:11434/v1"
      strategy: |
        Target young players entering their breakout window. Tolerate
        inconsistency in exchange for upside, and use the waiver wire
        weekly to chase hot streaks rather than holding underperforming
        stars.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := loadCreateSimPoolInputFromFile(configPath)
			if err != nil {
				return err
			}
			client, err := getGraphQLClient()
			if err != nil {
				return err
			}
			pool, err := client.CreateSimPool(cmd.Context(), input)
			if err != nil {
				return fmt.Errorf("create sim pool: %w", err)
			}
			// Print pool_id + workflow_id so scripts can capture them.
			// Workflow ID convention is "sim-pool-{id}" — same string
			// signal/cancel commands accept.
			fmt.Fprintf(cmd.OutOrStdout(), "pool_id=%d\nworkflow_id=sim-pool-%d\n", pool.ID, pool.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "",
		"Path to the YAML config file (required)")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

// loadCreateSimPoolInputFromFile reads + parses the YAML config.
// Pulled out as a non-method function so the test layer can exercise
// it without a Cobra command context.
func loadCreateSimPoolInputFromFile(path string) (*model.CreateSimPoolInput, error) {
	if path == "" {
		return nil, fmt.Errorf("--config is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()
	return parseCreateSimPoolInput(f)
}

// parseCreateSimPoolInput decodes YAML from r into the typed input.
// Strict decoding so a typo in the user's config surfaces immediately
// rather than as silently-ignored fields. sigs.k8s.io/yaml honors the
// gqlgen-generated json tags on model.CreateSimPoolInput, so the on-
// disk YAML uses the same field names the GraphQL schema does.
func parseCreateSimPoolInput(r io.Reader) (*model.CreateSimPoolInput, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read sim config: %w", err)
	}
	var input model.CreateSimPoolInput
	if err := yaml.UnmarshalStrict(data, &input); err != nil {
		return nil, fmt.Errorf("decode sim config: %w", err)
	}
	return &input, nil
}

// cmdSimCancel sends RequestCancelWorkflow to the sim's Temporal
// execution. The workflow's deferred cleanup writes status='cancelled'.
func cmdSimCancel() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <pool-id>",
		Short: "Cancel the sim (workflow exits with status=cancelled)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			poolID, err := parsePoolID(args[0])
			if err != nil {
				return err
			}
			client, err := getGraphQLClient()
			if err != nil {
				return err
			}
			pool, err := client.CancelSimPool(cmd.Context(), poolID)
			if err != nil {
				return fmt.Errorf("cancel: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pool_id=%d status=%s\n", pool.ID, pool.Status)
			return nil
		},
	}
}

// parsePoolID converts the positional arg to an int — extracted so
// every signal command shares one error message, and the parsing
// logic is unit-testable without a Cobra context.
func parsePoolID(s string) (int, error) {
	id, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid pool-id %q: %w", s, err)
	}
	if id <= 0 {
		return 0, fmt.Errorf("pool-id must be positive, got %d", id)
	}
	return id, nil
}

// ----------------------------------------------------------------------------
// status
// ----------------------------------------------------------------------------

func cmdSimStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status <pool-id>",
		Short: "Show pool summary, progress bar, and standings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			poolID, err := parsePoolID(args[0])
			if err != nil {
				return err
			}
			client, err := getGraphQLClient()
			if err != nil {
				return err
			}
			res, err := client.SimPoolStatus(cmd.Context(), poolID)
			if err != nil {
				return fmt.Errorf("sim status: %w", err)
			}
			renderSimPoolStatus(cmd.OutOrStdout(), res)
			return nil
		},
	}
}

// renderSimPoolStatus prints a four-section status block:
//
//  1. Header: pool ID + name (quoted).
//  2. Summary: aligned key-value block (season, status, sim_date, llm_cost).
//  3. Progress: per-group ASCII bars, or a "not started" notice.
//  4. Next step: status-aware command suggestion.
//  5. Standings: rank + quoted agent name + total roto points.
//
// Pure-function over the response — no I/O dependencies — so tests
// pass an in-memory buffer.
func renderSimPoolStatus(w io.Writer, r *SimPoolStatusResult) {
	if r == nil || r.Pool == nil {
		fmt.Fprintln(w, "(pool not found)")
		return
	}
	pool := r.Pool

	simDate := "(not started)"
	if pool.SimDate != nil {
		simDate = *pool.SimDate
	}
	fmt.Fprintf(w, "Pool %d: %q\n", pool.ID, pool.Name)
	fmt.Fprintf(w, "  season:   %d\n", pool.Season)
	fmt.Fprintf(w, "  status:   %s\n", pool.Status)
	fmt.Fprintf(w, "  sim_date: %s\n", simDate)
	fmt.Fprintf(w, "  llm_cost: $%.2f\n", pool.TotalLlmCostUsd)
	fmt.Fprintf(w, "  stop_after: %s\n", pool.StopAfter)
	if pool.MaxSeasonDays > 0 {
		fmt.Fprintf(w, "  max_season_days: %d\n", pool.MaxSeasonDays)
	}
	if pool.StartDate != nil {
		fmt.Fprintf(w, "  start_date: %s\n", *pool.StartDate)
	}
	if pool.EndDate != nil {
		fmt.Fprintf(w, "  end_date: %s\n", *pool.EndDate)
	}

	fmt.Fprintln(w)
	if r.Progress != nil {
		fmt.Fprintln(w, "Progress:")
		renderProgressGroups(w, r.Progress)
	} else {
		fmt.Fprintln(w, "(no progress reported yet — workflow may not have started)")
	}

	if a := pool.CurrentDraftAction; a != nil {
		fmt.Fprintln(w)
		// AgentID=0 is the pre-shuffle sentinel from currentDraftAction —
		// the pool is in draft status but the workflow hasn't assigned
		// draft positions yet, so we know the size but not who's on the
		// clock. Render the pick coordinate without an agent.
		if a.AgentID == 0 {
			fmt.Fprintf(w, "Drafting: Round %d, Pick %d of %d  (draft order not yet assigned)\n",
				a.Round, a.Pick, a.TotalPicks)
		} else {
			fmt.Fprintf(w, "Drafting: Round %d, Pick %d of %d  →  %s\n",
				a.Round, a.Pick, a.TotalPicks, agentDisplayName(pool.Agents, a.AgentID))
		}
	}

	if hint := nextStepHint(int(pool.ID), string(pool.Status)); hint != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, hint)
	}

	renderStandings(w, pool.Agents)
}

// nextStepHint returns a single line telling the user what to do next
// based on the pool's current status, or empty string if no hint applies.
func nextStepHint(poolID int, status string) string {
	switch status {
	case "draft", "running":
		return fmt.Sprintf("Next: ./puckdb sim tail --pool %d   (watch live) or ./puckdb sim status %d   (snapshot)", poolID, poolID)
	case "paused":
		return "Simulation paused (cost cap reached). Inspect via sim tail; create a new pool to continue."
	case "complete":
		return "Simulation complete."
	case "cancelled":
		return "Simulation was cancelled."
	default:
		return ""
	}
}

// renderProgressGroups prints one line per group with a small
// ASCII bar. Skipped groups (StartedAt == 0) hidden so a fresh
// pool's "Season" group doesn't show up before the draft completes.
func renderProgressGroups(w io.Writer, p *model.ProgressReport) {
	for _, g := range p.Groups {
		if g.StartedAt == 0 {
			continue
		}
		current, total := 0, 0
		for _, b := range g.Bars {
			current += b.Current
			total += b.Total
		}
		fmt.Fprintf(w, "  %-7s %s %d/%d\n", g.Header, asciiBar(current, total, simBarWidth), current, total)
	}
}

// renderStandings prints "rank. team_name   points" — one line per
// agent, sorted by total roto points descending. Ties broken by team
// name ascending for determinism. team_name is set by the Phase 0
// PickTeamName activity; when still empty (pre-draft or workflow
// died early) we render "agent #<id>" so each row still has a stable
// identifier.
func renderStandings(w io.Writer, agents []*model.SimAgent) {
	if len(agents) == 0 {
		return
	}
	sorted := make([]*model.SimAgent, len(agents))
	copy(sorted, agents)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].TotalRotoPoints != sorted[j].TotalRotoPoints {
			return sorted[i].TotalRotoPoints > sorted[j].TotalRotoPoints
		}
		return sorted[i].TeamName < sorted[j].TeamName
	})
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Standings:")
	for i, a := range sorted {
		name := a.TeamName
		if name == "" {
			name = fmt.Sprintf("agent #%d", a.ID)
		}
		fmt.Fprintf(w, "  %d. %-26s  %6.1f\n", i+1, fmt.Sprintf("%q", name), a.TotalRotoPoints)
	}
}

// agentDisplayName resolves an agent ID to its display name (team_name
// when set, "agent #<id>" otherwise). Pulled from the SimPool.agents
// array that the caller has already fetched — the GraphQL API returns
// agent IDs and lets the client do the display join.
func agentDisplayName(agents []*model.SimAgent, agentID int) string {
	for _, a := range agents {
		if a.ID == agentID {
			if a.TeamName != "" {
				return a.TeamName
			}
			return fmt.Sprintf("agent #%d", a.ID)
		}
	}
	return fmt.Sprintf("agent #%d", agentID)
}

// simBarWidth is the character width of the ASCII progress bar in
// the status output. 20 is wide enough to give each percentage point
// at least one character at 90% completion, narrow enough to fit on
// a typical terminal alongside the count text.
const simBarWidth = 20

// asciiBar renders a fixed-width "[#####     ]" bar. width-clamped
// to never exceed the configured width even with rounding.
func asciiBar(current, total, width int) string {
	if total <= 0 {
		return "[" + repeatRune(' ', width) + "]"
	}
	filled := current * width / total
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + repeatRune('#', filled) + repeatRune(' ', width-filled) + "]"
}

// repeatRune returns a string of n copies of r. Avoids strings.Repeat
// to keep the import surface small — this is the only place we
// build a fixed-rune string in this file.
func repeatRune(r rune, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}
