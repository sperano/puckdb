package draftsession

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
)

type persistedState struct {
	Upstream         []Pick            `json:"upstream"`
	Manual           []persistedManual `json:"manual"`
	UpstreamPlayers  []int             `json:"upstreamPlayers"`
	UpstreamComplete bool              `json:"upstreamComplete"`
	Version          uint64            `json:"version"`
}

type persistedManual struct {
	Key      PickKey    `json:"key"`
	Kind     ManualKind `json:"kind"`
	Pick     *Pick      `json:"pick,omitempty"`
	Base     *Pick      `json:"base,omitempty"`
	Conflict bool       `json:"conflict"`
}

// MarshalJSON stores map-backed session fields as sorted slices so persisted
// boards and recommendation inputs have one deterministic representation.
func (state State) MarshalJSON() ([]byte, error) {
	persisted := persistedState{
		Upstream:         make([]Pick, 0, len(state.Upstream)),
		Manual:           make([]persistedManual, 0, len(state.Manual)),
		UpstreamPlayers:  make([]int, 0, len(state.UpstreamPlayers)),
		UpstreamComplete: state.UpstreamComplete,
		Version:          state.Version,
	}
	for _, pick := range state.Upstream {
		persisted.Upstream = append(persisted.Upstream, pick)
	}
	for key, change := range state.Manual {
		persisted.Manual = append(persisted.Manual, persistedManual{
			Key: key, Kind: change.Kind, Pick: change.Pick, Base: change.Base, Conflict: change.Conflict,
		})
	}
	for playerID := range state.UpstreamPlayers {
		persisted.UpstreamPlayers = append(persisted.UpstreamPlayers, playerID)
	}
	slices.SortFunc(persisted.Upstream, func(left, right Pick) int { return comparePickKeys(left.Key, right.Key) })
	slices.SortFunc(persisted.Manual, func(left, right persistedManual) int {
		return comparePickKeys(left.Key, right.Key)
	})
	slices.Sort(persisted.UpstreamPlayers)
	return json.Marshal(persisted)
}

// UnmarshalJSON restores the map-backed session fields. It accepts both the
// current shape and stored recommendation inputs written before
// upstreamPlayers was added, plus the initial draft_sessions [] value.
func (state *State) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
		*state = emptyState()
		return nil
	}
	var persisted persistedState
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return err
	}
	decoded := emptyState()
	decoded.UpstreamComplete = persisted.UpstreamComplete
	decoded.Version = persisted.Version
	for _, pick := range persisted.Upstream {
		if _, exists := decoded.Upstream[pick.Key]; exists {
			return fmt.Errorf("duplicate persisted upstream slot %+v", pick.Key)
		}
		decoded.Upstream[pick.Key] = pick
	}
	for _, manual := range persisted.Manual {
		if _, exists := decoded.Manual[manual.Key]; exists {
			return fmt.Errorf("duplicate persisted manual slot %+v", manual.Key)
		}
		decoded.Manual[manual.Key] = ManualChange{
			Kind: manual.Kind, Pick: manual.Pick, Base: manual.Base, Conflict: manual.Conflict,
		}
	}
	for _, playerID := range persisted.UpstreamPlayers {
		decoded.UpstreamPlayers[playerID] = true
	}
	// Older rows did not persist upstreamPlayers. Current picks and manual
	// bases are the history that can still be recovered from those rows.
	RecordUpstreamPlayers(&decoded)
	*state = decoded
	return nil
}

func emptyState() State {
	return State{
		Upstream:        make(map[PickKey]Pick),
		Manual:          make(map[PickKey]ManualChange),
		UpstreamPlayers: make(map[int]bool),
	}
}

// comparePickKeys orders slots by round, then pick.
func comparePickKeys(left, right PickKey) int {
	return cmp.Or(cmp.Compare(left.Round, right.Round), cmp.Compare(left.Pick, right.Pick))
}
