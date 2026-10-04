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
