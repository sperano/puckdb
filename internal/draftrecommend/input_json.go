package draftrecommend

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sperano/puckdb/internal/draftsession"
)

type persistedSession struct {
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

// MarshalJSON stores the session maps as sorted slices. PickKey is a struct,
// so encoding the maps directly is unsupported by encoding/json.
func (in Input) MarshalJSON() ([]byte, error) {
	type inputAlias Input
	wire := struct {
		inputAlias
		Session persistedSession `json:"session"`
	}{inputAlias: inputAlias(in), Session: persistedSessionOf(in.Session)}
	return json.Marshal(wire)
}

// UnmarshalJSON restores the session maps used by deterministic replay.
func (in *Input) UnmarshalJSON(raw []byte) error {
	type inputAlias Input
	wire := struct {
		*inputAlias
		Session persistedSession `json:"session"`
	}{inputAlias: (*inputAlias)(in)}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	state, err := wire.Session.state()
	if err != nil {
		return err
	}
	in.Session = state
	return nil
}

func persistedSessionOf(state draftsession.State) persistedSession {
	stored := persistedSession{
		UpstreamComplete: state.UpstreamComplete, Version: state.Version,
		Upstream: make([]draftsession.Pick, 0, len(state.Upstream)),
		Manual:   make([]persistedManual, 0, len(state.Manual)),
	}
	for _, pick := range state.Upstream {
		stored.Upstream = append(stored.Upstream, pick)
	}
	for key, change := range state.Manual {
		stored.Manual = append(stored.Manual, persistedManual{
			Key: key, Kind: change.Kind, Pick: change.Pick, Base: change.Base, Conflict: change.Conflict,
		})
	}
	sort.Slice(stored.Upstream, func(i, j int) bool {
		return lessPickKey(stored.Upstream[i].Key, stored.Upstream[j].Key)
	})
	sort.Slice(stored.Manual, func(i, j int) bool {
		return lessPickKey(stored.Manual[i].Key, stored.Manual[j].Key)
	})
	return stored
}

func (stored persistedSession) state() (draftsession.State, error) {
	state := draftsession.State{
		Upstream:         make(map[draftsession.PickKey]draftsession.Pick),
		Manual:           make(map[draftsession.PickKey]draftsession.ManualChange),
		UpstreamComplete: stored.UpstreamComplete, Version: stored.Version,
	}
	for _, pick := range stored.Upstream {
		if _, exists := state.Upstream[pick.Key]; exists {
			return draftsession.State{}, fmt.Errorf("duplicate recommendation upstream slot %+v", pick.Key)
		}
		state.Upstream[pick.Key] = pick
	}
	for _, manual := range stored.Manual {
		if _, exists := state.Manual[manual.Key]; exists {
			return draftsession.State{}, fmt.Errorf("duplicate recommendation manual slot %+v", manual.Key)
		}
		state.Manual[manual.Key] = draftsession.ManualChange{
			Kind: manual.Kind, Pick: manual.Pick, Base: manual.Base, Conflict: manual.Conflict,
		}
	}
	return state, nil
}

func lessPickKey(left, right draftsession.PickKey) bool {
	if left.Round == right.Round {
		return left.Pick < right.Pick
	}
	return left.Round < right.Round
}
