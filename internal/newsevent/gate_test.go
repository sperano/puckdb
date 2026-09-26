package newsevent

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// zeroMetrics has every threshold ratio computed as 1 (nothing to measure),
// so Metrics{}.Passed() is true: a convenient stand-in for "a clean run".
var zeroMetrics = Metrics{}

func failingMetrics() Metrics {
	return Metrics{Cases: 1, Steps: 1, ExpectedEvents: 10, FoundEvents: 5, EmittedEvents: 5}
}

func TestRecordEvaluation_StoresMetricsAndPassed(t *testing.T) {
	db := newFakeDB()
	m := failingMetrics()
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	id, err := RecordEvaluation(context.Background(), db, "prov/model-a", Corpus{Version: "corpus-v1", Cases: []Case{{}}}, m, now)
	require.NoError(t, err)
	require.Len(t, db.evaluations, 1)

	row := db.evaluations[0]
	assert.Equal(t, id, row.ID)
	assert.Equal(t, "prov/model-a", row.ExtractorKey)
	assert.Equal(t, "corpus-v1", row.CorpusVersion)
	assert.Equal(t, int32(m.Cases), row.Cases)
	assert.False(t, row.Passed, "the constructed metrics miss the recall threshold")
	assert.True(t, row.RunAt.Valid)
	assert.Equal(t, now, row.RunAt.Time)

	var decoded Metrics
	require.NoError(t, json.Unmarshal(row.Metrics, &decoded))
	assert.Equal(t, m, decoded)
}

func TestAutomaticEffectsAllowed_NoRun(t *testing.T) {
	db := newFakeDB()
	allowed, err := AutomaticEffectsAllowed(context.Background(), db, "prov/model-a")
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestAutomaticEffectsAllowed_PassingRunOnBuiltinCorpus(t *testing.T) {
	db := newFakeDB()
	c, err := LoadCorpus("")
	require.NoError(t, err)

	_, err = RecordEvaluation(context.Background(), db, "prov/model-a", c, zeroMetrics, time.Now())
	require.NoError(t, err)

	allowed, err := AutomaticEffectsAllowed(context.Background(), db, "prov/model-a")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestAutomaticEffectsAllowed_LatestRunFailed(t *testing.T) {
	db := newFakeDB()
	c, err := LoadCorpus("")
	require.NoError(t, err)
	ctx := context.Background()
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := earlier.Add(24 * time.Hour)

	_, err = RecordEvaluation(ctx, db, "prov/model-a", c, zeroMetrics, earlier)
	require.NoError(t, err)
	_, err = RecordEvaluation(ctx, db, "prov/model-a", c, failingMetrics(), later)
	require.NoError(t, err)

	allowed, err := AutomaticEffectsAllowed(ctx, db, "prov/model-a")
	require.NoError(t, err)
	assert.False(t, allowed, "the most recent run, not the best one, gates automatic effects")
}

func TestAutomaticEffectsAllowed_OtherCorpusVersionDoesNotCount(t *testing.T) {
	db := newFakeDB()
	ctx := context.Background()
	_, err := RecordEvaluation(ctx, db, "prov/model-a", Corpus{Version: "not-the-builtin-version"}, zeroMetrics, time.Now())
	require.NoError(t, err)

	allowed, err := AutomaticEffectsAllowed(ctx, db, "prov/model-a")
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestAutomaticEffectsAllowed_OtherExtractorKeyDoesNotCount(t *testing.T) {
	db := newFakeDB()
	c, err := LoadCorpus("")
	require.NoError(t, err)
	ctx := context.Background()
	_, err = RecordEvaluation(ctx, db, "prov/other-model", c, zeroMetrics, time.Now())
	require.NoError(t, err)

	allowed, err := AutomaticEffectsAllowed(ctx, db, "prov/model-a")
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestWriteEvaluation_Passing(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEvaluation(&buf, "prov/model-a", "corpus-v1", zeroMetrics)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "| Measure | Value | Release threshold |")
	assert.Contains(t, out, "Event recall")
	assert.Contains(t, out, "Event precision")
	assert.Contains(t, out, "Field accuracy")
	assert.Contains(t, out, "Unsupported-claim rate")
	assert.Contains(t, out, "Invalid-output rate")
	assert.Contains(t, out, "Review recall")
	assert.Contains(t, out, "Lifecycle accuracy")
	assert.Contains(t, out, "Injection failures")
	assert.Contains(t, out, "**Passed** every release threshold.")
	assert.NotContains(t, out, "## Misses")
}

func TestWriteEvaluation_FailingListsShortfallsAndMisses(t *testing.T) {
	m := failingMetrics()
	m.Failures = []string{"case-1 step 1: missed P1 injury reported"}
	var buf bytes.Buffer
	err := WriteEvaluation(&buf, "prov/model-a", "corpus-v1", m)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "**Failed:**")
	assert.Contains(t, out, "event recall")
	assert.Contains(t, out, "Automatic numeric effects stay off for this extractor.")
	assert.Contains(t, out, "## Misses")
	assert.Contains(t, out, "case-1 step 1: missed P1 injury reported")
	assert.NotContains(t, out, "**Passed**")
}
