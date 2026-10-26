package draftsession

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	currentStateJSON              = `{"upstream":[{"Key":{"Round":1,"Pick":1},"TeamID":2,"PlayerID":5,"Cost":null},{"Key":{"Round":1,"Pick":2},"TeamID":3,"PlayerID":4,"Cost":null}],"manual":[{"key":{"Round":2,"Pick":3},"kind":"undo","base":{"Key":{"Round":2,"Pick":3},"TeamID":8,"PlayerID":9,"Cost":null},"conflict":false},{"key":{"Round":3,"Pick":1},"kind":"add","pick":{"Key":{"Round":3,"Pick":1},"TeamID":7,"PlayerID":6,"Cost":null},"conflict":true}],"upstreamPlayers":[4,5,9],"upstreamComplete":true,"version":7}`
	legacyRecommendationStateJSON = `{"upstream":[{"Key":{"Round":1,"Pick":1},"TeamID":2,"PlayerID":5,"Cost":null},{"Key":{"Round":1,"Pick":2},"TeamID":3,"PlayerID":4,"Cost":null}],"manual":[{"key":{"Round":2,"Pick":3},"kind":"undo","base":{"Key":{"Round":2,"Pick":3},"TeamID":8,"PlayerID":9,"Cost":null},"conflict":false},{"key":{"Round":3,"Pick":1},"kind":"add","pick":{"Key":{"Round":3,"Pick":1},"TeamID":7,"PlayerID":6,"Cost":null},"conflict":true}],"upstreamComplete":true,"version":7}`
)

func TestStateJSONStorageShapesDecodeEquivalently(t *testing.T) {
	current := decodeStateJSON(t, currentStateJSON)
	legacyRecommendation := decodeStateJSON(t, legacyRecommendationStateJSON)

	assert.Equal(t, current, legacyRecommendation)
	raw, err := json.Marshal(legacyRecommendation)
	require.NoError(t, err)
	assert.Equal(t, currentStateJSON, string(raw))
}

func TestStateJSONRoundTripUsesDeterministicGoldenShape(t *testing.T) {
	state := decodeStateJSON(t, currentStateJSON)

	raw, err := json.Marshal(state)
	require.NoError(t, err)
	assert.Equal(t, currentStateJSON, string(raw))

	var roundTrip State
	require.NoError(t, json.Unmarshal(raw, &roundTrip))
	assert.Equal(t, state, roundTrip)
}

func TestStateJSONAcceptsInitialLegacyEmptyArray(t *testing.T) {
	var state State
	require.NoError(t, json.Unmarshal([]byte(`[]`), &state))
	assert.Empty(t, state.Upstream)
	assert.Empty(t, state.Manual)
	assert.Empty(t, state.UpstreamPlayers)
}

func TestStateJSONRejectsDuplicateSlots(t *testing.T) {
	raw := []byte(`{"upstream":[{"Key":{"Round":1,"Pick":1}},{"Key":{"Round":1,"Pick":1}}],"manual":[]}`)

	var state State
	err := json.Unmarshal(raw, &state)

	require.Error(t, err)
	assert.ErrorContains(t, err, "duplicate persisted upstream slot")
}

func decodeStateJSON(t *testing.T, raw string) State {
	t.Helper()
	var state State
	require.NoError(t, json.Unmarshal([]byte(raw), &state))
	return state
}
