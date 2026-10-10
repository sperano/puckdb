package newsevent

// Replays the evaluation corpus with its reference replies through both
// stores at once: PostgreSQL via LoadNear and Apply, as the worker runs, and
// MemoryStore via Near and Apply, as the release gate measures. The fixture
// plumbing is in store_pg_test.go.

import (
	"cmp"
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPGCorpusReplayMatchesMemoryStore checks, at every step of every case,
// that the two stores return the same events near the report, plan the same
// changes, report the same outcome and hold the same events afterwards. The
// corpus covers supersession, late reports, retraction by denial and by
// omission, reinstatement, syndicated duplicates and conflicts.
func TestPGCorpusReplayMatchesMemoryStore(t *testing.T) {
	pool := openNewsEventPGTestDB(t)
	q := sqlcdb.New(pool)
	c, err := LoadCorpus("")
	require.NoError(t, err)
	x := Extractor{Client: ReferenceClient(c, evalMaxInputRunes), Provider: "reference", Model: "labels", MaxOutputTokens: evalMaxOutputTokens}
	for _, tc := range c.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			resetNewsEventTables(t, pool)
			mem := &MemoryStore{}
			cr := newCaseRun(tc)
			for i := range tc.Steps {
				replayStep(t, q, pool, x, cr, i, mem)
			}
		})
	}
}

// replayStep reconciles the case's i-th article version against both stores.
func replayStep(t *testing.T, q *sqlcdb.Queries, pool *pgxpool.Pool, x Extractor, cr *caseRun, i int, mem *MemoryStore) {
	t.Helper()
	ctx := context.Background()
	in, r := cr.step(i, evalMaxInputRunes)
	// The evaluation harness records no extraction; the evidence rows need one.
	r.ExtractionID = r.VersionID
	seedReportRows(t, pool, r)
	v := referenceValidation(t, x, in)
	r.Clean = v.Clean()
	players := playersOf(in)

	pgNear, err := LoadNear(ctx, q, players, r, evalWindow)
	require.NoError(t, err, "step %d", i+1)
	memNear := mem.Near(players, r, evalWindow)
	assertSameNear(t, memNear, pgNear)

	pgPlan := Reconcile(pgNear, r, v.Events, evalWindow)
	memPlan := Reconcile(memNear, r, v.Events, evalWindow)
	assert.Equal(t, comparablePlan(memPlan), comparablePlan(pgPlan), "step %d plan", i+1)

	pgOut := applyInTx(t, q, pool, pgPlan, r)
	memOut := mem.Apply(memPlan, r)
	assert.Equal(t, memOut, pgOut, "step %d outcome", i+1)
	assertSameStore(t, mem, q)
}

// referenceValidation validates the corpus's reference reply for an input.
func referenceValidation(t *testing.T, x Extractor, in Input) Validation {
	t.Helper()
	reply, err := x.Call(context.Background(), in)
	require.NoError(t, err)
	require.NoError(t, reply.Rejected)
	v, err := Interpret(reply.Content, in)
	require.NoError(t, err)
	return v
}

// applyInTx applies a plan in one transaction, as the worker does.
func applyInTx(t *testing.T, q *sqlcdb.Queries, pool *pgxpool.Pool, plan Plan, r Report) Outcome {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	out, err := Apply(ctx, q.WithTx(tx), plan, r, pgNow)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return out
}

// comparablePlan orders the parts of a plan that follow the order of the
// events on record, which the two stores list differently (the SQL by first
// report, the memory store by ID). Apply's result does not depend on it.
func comparablePlan(p Plan) Plan {
	p.Evidence = slices.Clone(p.Evidence)
	slices.SortStableFunc(p.Evidence, func(a, b EvidenceLink) int {
		return cmp.Or(cmp.Compare(a.Ref.ID, b.Ref.ID), cmp.Compare(a.Ref.New, b.Ref.New))
	})
	p.Transitions = slices.Clone(p.Transitions)
	slices.SortStableFunc(p.Transitions, func(a, b Transition) int { return cmp.Compare(a.EventID, b.EventID) })
	p.Reviews = slices.Clone(p.Reviews)
	slices.SortStableFunc(p.Reviews, func(a, b ReviewFlag) int { return cmp.Compare(a.EventID, b.EventID) })
	p.Touch = slices.Clone(p.Touch)
	slices.Sort(p.Touch)
	return p
}
