package workflow

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/workflow"
)

// newImportEdgeParent stubs each ImportEdgeWorkflow child to record its ID
// and input and to return one import per bar unit; the tests check which
// children started.
func newImportEdgeParent(t *testing.T, seasons []nhl.SeasonInfo) *fanOutParent {
	p := newFanOutParent(t, ImportEdgeSeasonsWorkflow, seasons)
	p.env.RegisterWorkflow(ImportEdgeWorkflow)
	p.env.OnWorkflow(ImportEdgeWorkflow, mock.Anything, mock.Anything).Return(
		func(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
			p.recordChild(ctx, season)
			return core.OriginCounts{core.OriginFileSystem: edgeTestBarTotal}, nil
		}).Maybe()
	return p
}

// ImportEdgeSeasonsWorkflow imports only the Edge-eligible seasons, each
// child receiving its season, and keys each bar and stale report to the
// child's workflow ID.
func TestImportEdgeSeasonsWorkflowImportsEligibleSeasons(t *testing.T) {
	p := newImportEdgeParent(t, edgeTestManifest())
	group := p.run(t, ImportEdgeSeasonsWorkflow, nil)

	var want []fanOutChild
	for _, s := range edgeTestEligible() {
		want = append(want, fanOutChild{WorkflowIDImportEdge(s.ID.StartYear()), s})
	}
	assert.ElementsMatch(t, want, p.children)
	assert.Equal(t, fanOutSourceKeys(edgeTestEligible(), WorkflowIDImportEdge), p.staleKeys)
	assert.Equal(t, fanOutBars(edgeTestEligible(), edgeTestBar, WorkflowIDImportEdge), group.Bars)
	elapsed := shared.FormatElapsedMilli(group.CompletedAt - group.StartedAt)
	assert.Equal(t, "Imported Edge stats for 2 seasons in "+elapsed+". (imports: 100% filesystem)",
		group.CompletedMsg)
}

// A manifest without an Edge-eligible season starts nothing: no stale
// report cleanup, no bar, no child and no completion.
func TestImportEdgeSeasonsWorkflowWithoutEligibleSeason(t *testing.T) {
	p := newImportEdgeParent(t, edgeTestManifest()[:2])
	group := p.run(t, ImportEdgeSeasonsWorkflow, nil)

	assert.Empty(t, p.children)
	assert.Empty(t, p.staleKeys)
	assert.Empty(t, group.Bars)
	assert.Zero(t, group.StartedAt)
	assert.Empty(t, group.CompletedMsg)
}
