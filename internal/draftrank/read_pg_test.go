package draftrank_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRead_ComparesStoredRulesWithAStoredSnapshot runs the shared read over
// PostgreSQL: rules and snapshot both round-trip through their JSON columns,
// so an unchanged league must not look changed, and a re-import must be
// classified by what changed.
func TestRead_ComparesStoredRulesWithAStoredSnapshot(t *testing.T) {
	pool := openDraftTestDB(t)
	resetHistory(t, pool)
	t.Cleanup(func() { resetHistory(t, pool) })
	seedLeague(t, pool, seed{rules: true, pool: true, history: true, seasonDates: true})
	outcome, err := refreshFixture(t, pool, refreshRunID)
	require.NoError(t, err)
	require.Equal(t, draftrank.RefreshSucceeded, outcome.State, outcome.Error)
	svc := draftrank.NewService(draftrank.NewPGStore(pool), nil,
		draftrank.ServiceOptions{Now: func() time.Time { return draftfixtures.AsOf.Add(time.Hour) }})
	q := sqlcdb.New(pool)

	steps := []struct {
		name   string
		change func(*draft.Rules)
		codes  []draftrank.IssueCode
		advice draftrank.Advice
	}{
		{"unchanged rules", nil, nil, draftrank.AdviceUnaffected},
		{"draft started", func(r *draft.Rules) { r.Draft.Status = "draft" },
			[]draftrank.IssueCode{draftrank.IssueRulesChanged}, draftrank.AdviceOutdated},
		{"one more center", func(r *draft.Rules) { r.Draft.Status = "draft"; r.RosterSlots[0].Count++ },
			[]draftrank.IssueCode{draftrank.IssueRulesChanged, draftrank.IssueRosterSlotsChanged}, draftrank.AdviceWithheld},
	}
	for _, step := range steps {
		if step.change != nil {
			rules := draftfixtures.Rules().Rules
			step.change(&rules)
			upsertRules(t, q, rules)
		}
		read, err := svc.Read(context.Background(), fixtureRef(), uuid.Nil)
		require.NoError(t, err, step.name)
		require.NotNil(t, read.Snapshot, step.name)
		assert.Equal(t, outcome.SnapshotID, read.Snapshot.ID, step.name)
		var dynamic []draftrank.IssueCode
		for _, code := range issueCodes(read.Issues) {
			if code == draftrank.IssueRulesChanged || code == draftrank.IssueRosterSlotsChanged || code == draftrank.IssueScoringChanged {
				dynamic = append(dynamic, code)
			}
		}
		assert.ElementsMatch(t, step.codes, dynamic, step.name)
		assert.Equal(t, step.advice, draftrank.AdviceOf(read.Issues), step.name)
	}
}
