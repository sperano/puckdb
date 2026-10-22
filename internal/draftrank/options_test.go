package draftrank_test

import (
	"encoding/json"
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptionsPolicyTypesPreserveJSONShape(t *testing.T) {
	options := draftrank.Options{
		BenchPolicy:        draft.BenchExcluded,
		WorkloadCapPolicy:  draft.WorkloadCapsPerPlayer,
		UncertaintyPenalty: 0.5,
	}

	raw, err := json.Marshal(options)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"benchPolicy":"excluded",
		"workloadCapPolicy":"per-player",
		"uncertaintyPenalty":0.5
	}`, string(raw))

	var decoded draftrank.Options
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, options, decoded)
}
