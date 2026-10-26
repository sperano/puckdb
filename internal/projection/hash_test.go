package projection

import (
	"math"
	"testing"

	"github.com/sperano/puckdb/internal/canonhash"
	"github.com/stretchr/testify/require"
)

func mustConfigHash(t testing.TB, cfg Config) string {
	t.Helper()
	hash, err := configHash(cfg)
	require.NoError(t, err)
	return hash
}

func mustSourceDataHash(t testing.TB, cfg Config, input Input) string {
	t.Helper()
	hash, err := sourceDataHash(cfg, input)
	require.NoError(t, err)
	return hash
}

func mustEvaluationDataHash(t testing.TB, cfg Config, input Input) string {
	t.Helper()
	hash, err := evaluationDataHash(cfg, input)
	require.NoError(t, err)
	return hash
}

func mustInputDataHash(t testing.TB, input Input, modelVersion string) string {
	t.Helper()
	hash, err := inputDataHash(input, modelVersion)
	require.NoError(t, err)
	return hash
}

// mustHashJSON hashes a value the way the projection hashes its configs and
// canonical inputs.
func mustHashJSON(t testing.TB, value any) string {
	t.Helper()
	hash, err := canonhash.JSON(value)
	require.NoError(t, err)
	return hash
}

// A NaN read from the database (PostgreSQL float8 stores it) fails the hash,
// and so the generation or evaluation, instead of panicking the worker.
func TestSourceHashesRejectNonFiniteRows(t *testing.T) {
	input := pinnedInput()
	input.Skaters[0].LinematePointsPer60 = math.NaN()
	cfg := versionConfig(ModelVersion)
	_, err := sourceDataHash(cfg, input)
	require.ErrorContains(t, err, "unsupported value")
	_, err = evaluationDataHash(cfg, input)
	require.Error(t, err)
	_, err = Generate(cfg, input)
	require.Error(t, err)
	_, err = Evaluate(cfg, input)
	require.Error(t, err)
}

func TestSourceHashesRejectNonFiniteOverride(t *testing.T) {
	input := pinnedInput()
	input.Overrides = []Override{{PlayerKey: "nhl:1", Values: map[Stat]Estimate{StatGoals: {Mean: math.Inf(1)}}}}
	_, err := sourceDataHash(versionConfig(ModelVersion), input)
	require.ErrorContains(t, err, "unsupported value")
}

func TestConfigHashRejectsNonFiniteSetting(t *testing.T) {
	cfg := versionConfig(ModelVersion)
	cfg.GoalieShareBlend = math.NaN()
	_, err := configHash(cfg)
	require.ErrorContains(t, err, "hash projection config")
	_, err = snapshotParams(Snapshot{Config: cfg})
	require.Error(t, err)
}
