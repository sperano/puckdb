package draftrecommend

import (
	"encoding/json"
	"testing"

	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInputJSONRoundTripPreservesReplaySession(t *testing.T) {
	key := draftsession.PickKey{Round: 1, Pick: 2}
	pick := draftsession.Pick{Key: key, TeamID: 3, PlayerID: 4}
	in := Input{Session: draftsession.State{
		Upstream: map[draftsession.PickKey]draftsession.Pick{key: pick},
		Manual: map[draftsession.PickKey]draftsession.ManualChange{
			{Round: 2, Pick: 3}: {Kind: draftsession.ManualUndo, Base: &pick},
		},
		UpstreamComplete: true,
		Version:          7,
	}}

	raw, err := json.Marshal(in)
	require.NoError(t, err)

	var replay Input
	require.NoError(t, json.Unmarshal(raw, &replay))
	assert.Equal(t, in.Session, replay.Session)
}

func TestInputJSONRejectsDuplicateSessionSlots(t *testing.T) {
	raw := []byte(`{"session":{"upstream":[{"Key":{"Round":1,"Pick":1},"TeamID":2,"PlayerID":3},{"Key":{"Round":1,"Pick":1},"TeamID":2,"PlayerID":4}],"manual":[],"upstreamComplete":true,"version":1}}`)

	var input Input
	err := json.Unmarshal(raw, &input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate recommendation upstream slot")
}
