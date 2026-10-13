package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/cobra"
)

// ============================================================================
// `puckdb sim draft <pool-id>` — print one section per agent showing their
// draft picks in pick order. Reads the API's simDraftPicks query (the
// sim_transactions + sim_agents + players join runs server-side).
//
// Layout: agents sorted by draft_position (NULLS last for pre-shuffle); within
// each agent, picks sorted by round + pick. Reasoning is truncated to keep
// rows readable; pass `--reasoning` to see the full text.
// ============================================================================

const draftReasoningPreviewMax = 70

// errSimPoolNotFound is what a simDraftLister returns for a pool that does
// not exist.
var errSimPoolNotFound = errors.New("sim pool not found")

// simDraftLister reads a pool's draft picks; *GraphQLClient implements it.
type simDraftLister interface {
	SimDraftPicks(ctx context.Context, poolID int) ([]draftRow, error)
}

func cmdSimDraft() *cobra.Command {
	var fullReasoning bool
	cmd := &cobra.Command{
		Use:     "draft <pool-id>",
		Short:   "Show draft picks grouped by agent",
		Args:    cobra.ExactArgs(1),
		PreRunE: bindFlagsPreRunE(simClientFlagGroups...),
		RunE: func(cmd *cobra.Command, args []string) error {
			poolID, err := parsePoolID(args[0])
			if err != nil {
				return err
			}
			client, err := getGraphQLClient()
			if err != nil {
				return err
			}
			return renderSimDraft(cmd.Context(), client, cmd.OutOrStdout(), poolID, fullReasoning)
		},
	}
	cmd.Flags().BoolVar(&fullReasoning, "reasoning", false, "Show full per-pick reasoning instead of a one-line preview")
	config.InitFlags(cmd.Flags(), simClientFlagGroups...)
	return cmd
}

// draftRow is one simDraftPicks entry — one draft_pick transaction
// projected with its agent identity and the picked player's name +
// position. Empty player_name happens when the player FK no longer
// resolves (player row deleted post-draft; shouldn't happen in normal
// operation but the server's LEFT JOIN tolerates it).
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

func renderSimDraft(ctx context.Context, lister simDraftLister, w io.Writer, poolID int, fullReasoning bool) error {
	rows, err := lister.SimDraftPicks(ctx, poolID)
	if errors.Is(err, errSimPoolNotFound) {
		return fmt.Errorf("pool %d not found", poolID)
	}
	if err != nil {
		return fmt.Errorf("sim draft picks: %w", err)
	}
	if len(rows) == 0 {
		fmt.Fprintf(w, "Pool %d: no draft picks yet.\n", poolID)
		return nil
	}

	// Group by agent in the order rows arrive — the server's ORDER BY
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
