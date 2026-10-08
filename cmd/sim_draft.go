package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/cobra"
)

// ============================================================================
// `puckdb sim draft <pool-id>` — print one section per agent showing their
// draft picks in pick order. Joins sim_transactions + sim_agents + players
// directly against Postgres (same pattern as `sim tail`).
//
// Layout: agents sorted by draft_position (NULLS last for pre-shuffle); within
// each agent, picks sorted by round + pick. Reasoning is truncated to keep
// rows readable; pass `--reasoning` to see the full text.
// ============================================================================

const draftReasoningPreviewMax = 70

// simDraftFlagGroups lists every flag group `sim draft` exposes. Defined
// once and shared by InitFlags and BindFlags so the two can never drift.
var simDraftFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
}

func cmdSimDraft() *cobra.Command {
	var fullReasoning bool
	cmd := &cobra.Command{
		Use:   "draft <pool-id>",
		Short: "Show draft picks grouped by agent",
		Args:  cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), simDraftFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			poolID, err := parsePoolID(args[0])
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			pool, err := openPGXPool(ctx)
			if err != nil {
				return fmt.Errorf("open db pool: %w", err)
			}
			defer pool.Close()
			return renderSimDraft(ctx, pool, cmd.OutOrStdout(), int32(poolID), fullReasoning)
		},
	}
	cmd.Flags().BoolVar(&fullReasoning, "reasoning", false, "Show full per-pick reasoning instead of a one-line preview")
	config.InitFlags(cmd.Flags(), simDraftFlagGroups...)
	return cmd
}

// draftRow is one row of the SQL join — one draft_pick transaction
// projected with its agent identity and the picked player's name +
// position. Empty player_name happens when the player FK no longer
// resolves (player row deleted post-draft; shouldn't happen in normal
// operation but the LEFT JOIN tolerates it).
type draftRow struct {
	AgentID       int32
	TeamName      string
	Summary       string
	Model         string
	DraftPosition *int32 // nil = pre-shuffle (shouldn't appear once draft has run)
	Round         int32
	Pick          int32
	PlayerID      int64
	PlayerName    string
	Position      string
	Reasoning     string
}

func renderSimDraft(ctx context.Context, pool *pgxpool.Pool, w io.Writer, poolID int32, fullReasoning bool) error {
	rows, err := selectDraftRows(ctx, pool, poolID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintf(w, "Pool %d: no draft picks yet.\n", poolID)
		return nil
	}

	// Group by agent in the order rows arrive — the SQL ORDER BY
	// guarantees agents come out in draft_position order with picks
	// sorted within each agent.
	type group struct {
		row   draftRow // shared agent fields
		picks []draftRow
	}
	groups := make([]*group, 0)
	byAgent := map[int32]*group{}
	for _, r := range rows {
		g, ok := byAgent[r.AgentID]
		if !ok {
			g = &group{row: r}
			byAgent[r.AgentID] = g
			groups = append(groups, g)
		}
		g.picks = append(g.picks, r)
	}

	fmt.Fprintf(w, "Pool %d draft results — %d teams, %d picks\n\n", poolID, len(groups), len(rows))
	for i, g := range groups {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s\n", agentLabel(g.row.AgentID, g.row.TeamName, g.row.Summary, g.row.Model))
		for _, p := range g.picks {
			fmt.Fprintln(w, formatDraftPickLine(p, fullReasoning))
		}
	}
	return nil
}

// formatDraftPickLine renders one pick: "  R1.P3  C   Connor McDavid    reason..."
// Aligned columns for readability; reasoning is the variable-width tail.
func formatDraftPickLine(r draftRow, fullReasoning bool) string {
	playerCol := r.PlayerName
	if playerCol == "" {
		playerCol = fmt.Sprintf("player #%d", r.PlayerID)
	}
	reason := r.Reasoning
	if !fullReasoning && len(reason) > draftReasoningPreviewMax {
		reason = reason[:draftReasoningPreviewMax-3] + "..."
	}
	if reason != "" {
		return fmt.Sprintf("  R%d.P%-2d  %-3s  %-25s  %s", r.Round, r.Pick, r.Position, playerCol, reason)
	}
	return fmt.Sprintf("  R%d.P%-2d  %-3s  %s", r.Round, r.Pick, r.Position, playerCol)
}

func selectDraftRows(ctx context.Context, pool *pgxpool.Pool, poolID int32) ([]draftRow, error) {
	// LEFT JOIN to players so a deleted player row doesn't drop the
	// whole pick from the output — the agent still made a pick, even
	// if the name lookup fails. Position falls back to '?' for the
	// same reason. ORDER BY agent's draft_position so the snake-order
	// reading is intuitive (first overall pick's agent listed first).
	const sql = `
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

	rs, err := pool.Query(ctx, sql, poolID)
	if err != nil {
		return nil, fmt.Errorf("select draft rows: %w", err)
	}
	defer rs.Close()

	var out []draftRow
	for rs.Next() {
		var r draftRow
		var round, pick int32
		if err := rs.Scan(&r.AgentID, &r.TeamName, &r.Summary, &r.Model, &r.DraftPosition,
			&round, &pick, &r.PlayerID, &r.PlayerName, &r.Position, &r.Reasoning); err != nil {
			return nil, fmt.Errorf("scan draft row: %w", err)
		}
		r.Round = round
		r.Pick = pick
		r.PlayerName = strings.TrimSpace(r.PlayerName)
		out = append(out, r)
	}
	return out, rs.Err()
}
