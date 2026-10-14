package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDraftLister returns fixed rows or a fixed error.
type fakeDraftLister struct {
	rows []draftRow
	err  error
}

func (f fakeDraftLister) SimDraftPicks(context.Context, int) ([]draftRow, error) {
	return f.rows, f.err
}

// Picks group by agent in arrival order (the server sorts by draft
// position); a pick with no player prints its id, a long reasoning is cut.
func TestRenderSimDraft_GroupsByAgent(t *testing.T) {
	t.Parallel()
	first := int32(1)
	rows := []draftRow{
		{AgentID: 2, Model: "claude-haiku", DraftPosition: &first, Round: 1, Pick: 1, PlayerName: "Ovechkin", Position: "C", Reasoning: "Goals."},
		{AgentID: 2, Model: "claude-haiku", DraftPosition: &first, Round: 2, Pick: 4, PlayerName: "Pos Less", Position: "?"},
		{AgentID: 1, TeamName: "Stars", Summary: "balanced", Round: 1, Pick: 2, PlayerID: 7, Position: "?",
			Reasoning: "Generational talent; unmatched production unmatched production unmatched production"},
	}
	var out bytes.Buffer
	require.NoError(t, renderSimDraft(context.Background(), fakeDraftLister{rows: rows}, &out, 1, false))
	assert.Equal(t, `Pool 1 draft results — 2 teams, 3 picks

agent #2
  R1.P1   C    Ovechkin                   Goals.
  R2.P4   ?    Pos Less

Stars (balanced)
  R1.P2   ?    player #7                  Generational talent; unmatched production unmatched production unma...
`, out.String())
}

func TestRenderSimDraft_NoPicksYet(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	require.NoError(t, renderSimDraft(context.Background(), fakeDraftLister{}, &out, 4, false))
	assert.Equal(t, "Pool 4: no draft picks yet.\n", out.String())
}

func TestRenderSimDraft_MissingPool(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := renderSimDraft(context.Background(), fakeDraftLister{err: errSimPoolNotFound}, &out, 4, false)
	require.EqualError(t, err, "pool 4 not found")
	assert.Empty(t, out.String())
}

func TestRenderSimDraft_ListError(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	err := renderSimDraft(context.Background(), fakeDraftLister{err: boom}, &bytes.Buffer{}, 4, false)
	require.ErrorIs(t, err, boom)
}
