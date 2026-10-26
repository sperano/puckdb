package draftwatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sperano/puckdb/internal/draftsession"
)

func encodeState(state draftsession.State) ([]byte, string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, "", fmt.Errorf("marshal draft session state: %w", err)
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
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
