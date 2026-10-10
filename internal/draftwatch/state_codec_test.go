package draftwatch

import (
	"testing"

	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateCodecRoundTripPreservesRestartState(t *testing.T) {
	key := draftsession.PickKey{Round: 2, Pick: 13}
	base := draftsession.Pick{Key: key, TeamID: 3, PlayerID: 101}
	manual := draftsession.Pick{Key: key, TeamID: 3, PlayerID: 202}
	state := draftsession.State{
		Upstream: map[draftsession.PickKey]draftsession.Pick{key: base},
		Manual: map[draftsession.PickKey]draftsession.ManualChange{
			key: {Kind: draftsession.ManualCorrect, Pick: &manual, Base: &base, Conflict: true},
		},
		UpstreamPlayers:  map[int]bool{101: true, 99: true},
		UpstreamComplete: true,
		Version:          9,
	}

	raw, firstHash, err := encodeState(state)
	require.NoError(t, err)
	restarted, err := decodeState(raw)
	require.NoError(t, err)
	secondRaw, secondHash, err := encodeState(restarted)
	require.NoError(t, err)

	assert.JSONEq(t, string(raw), string(secondRaw))
	assert.Equal(t, firstHash, secondHash)
	assert.Equal(t, state, restarted)
}

func TestDecodeStateAcceptsInitialLegacyEmptyArray(t *testing.T) {
	state, err := decodeState([]byte(`[]`))
	require.NoError(t, err)
	assert.Empty(t, state.Upstream)
	assert.Empty(t, state.Manual)
}

func TestDecodeStateSeedsUpstreamPlayersOfLegacyBoard(t *testing.T) {
	legacy := `{"upstream":[{"Key":{"Round":1,"Pick":1},"TeamID":3,"PlayerID":102,"Cost":null}],` +
		`"manual":[{"key":{"Round":1,"Pick":2},"kind":"undo","base":{"Key":{"Round":1,"Pick":2},"TeamID":8,"PlayerID":105,"Cost":null},"conflict":false}],` +
		`"upstreamComplete":true,"version":4}`

	state, err := decodeState([]byte(legacy))

	require.NoError(t, err)
	assert.Equal(t, map[int]bool{102: true, 105: true}, state.UpstreamPlayers)
	raw, _, err := encodeState(state)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"upstreamPlayers":[102,105]`)
}
