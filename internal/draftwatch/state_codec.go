package draftwatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sperano/puckdb/internal/draftsession"
)

type persistedState struct {
	Upstream         []draftsession.Pick `json:"upstream"`
	Manual           []persistedManual   `json:"manual"`
	UpstreamComplete bool                `json:"upstreamComplete"`
	Version          uint64              `json:"version"`
}

type persistedManual struct {
	Key      draftsession.PickKey    `json:"key"`
	Kind     draftsession.ManualKind `json:"kind"`
	Pick     *draftsession.Pick      `json:"pick,omitempty"`
	Base     *draftsession.Pick      `json:"base,omitempty"`
	Conflict bool                    `json:"conflict"`
}

func encodeState(state draftsession.State) ([]byte, string, error) {
	persisted := persistedState{
		UpstreamComplete: state.UpstreamComplete,
		Version:          state.Version,
		Upstream:         make([]draftsession.Pick, 0, len(state.Upstream)),
		Manual:           make([]persistedManual, 0, len(state.Manual)),
	}
	for _, pick := range state.Upstream {
		persisted.Upstream = append(persisted.Upstream, pick)
	}
	for key, change := range state.Manual {
		persisted.Manual = append(persisted.Manual, persistedManual{
			Key: key, Kind: change.Kind, Pick: change.Pick, Base: change.Base, Conflict: change.Conflict,
		})
	}
	sort.Slice(persisted.Upstream, func(i, j int) bool {
		return lessPickKey(persisted.Upstream[i].Key, persisted.Upstream[j].Key)
	})
	sort.Slice(persisted.Manual, func(i, j int) bool {
		return lessPickKey(persisted.Manual[i].Key, persisted.Manual[j].Key)
	})
	raw, err := json.Marshal(persisted)
	if err != nil {
		return nil, "", fmt.Errorf("marshal draft session state: %w", err)
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func decodeState(raw []byte) (draftsession.State, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
		return newState(), nil
	}
	var persisted persistedState
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return draftsession.State{}, fmt.Errorf("unmarshal draft session state: %w", err)
	}
	state := newState()
	state.UpstreamComplete = persisted.UpstreamComplete
	state.Version = persisted.Version
	for _, pick := range persisted.Upstream {
		if _, exists := state.Upstream[pick.Key]; exists {
			return draftsession.State{}, fmt.Errorf("duplicate persisted upstream slot %+v", pick.Key)
		}
		state.Upstream[pick.Key] = pick
	}
	for _, manual := range persisted.Manual {
		if _, exists := state.Manual[manual.Key]; exists {
			return draftsession.State{}, fmt.Errorf("duplicate persisted manual slot %+v", manual.Key)
		}
		state.Manual[manual.Key] = draftsession.ManualChange{
			Kind: manual.Kind, Pick: manual.Pick, Base: manual.Base, Conflict: manual.Conflict,
		}
	}
	return state, nil
}

func newState() draftsession.State {
	return draftsession.State{
		Upstream: make(map[draftsession.PickKey]draftsession.Pick),
		Manual:   make(map[draftsession.PickKey]draftsession.ManualChange),
	}
}

func lessPickKey(left, right draftsession.PickKey) bool {
	if left.Round == right.Round {
		return left.Pick < right.Pick
	}
	return left.Round < right.Round
}
