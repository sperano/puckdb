package draftwatch

import (
	"encoding/json"
	"fmt"

	"github.com/sperano/puckdb/internal/canonhash"
	"github.com/sperano/puckdb/internal/draftsession"
)

func encodeState(state draftsession.State) ([]byte, string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, "", fmt.Errorf("marshal draft session state: %w", err)
	}
	return raw, canonhash.Bytes(raw), nil
}

func decodeState(raw []byte) (draftsession.State, error) {
	if len(raw) == 0 {
		raw = []byte("[]")
	}
	var state draftsession.State
	if err := json.Unmarshal(raw, &state); err != nil {
		return draftsession.State{}, fmt.Errorf("unmarshal draft session state: %w", err)
	}
	return state, nil
}
