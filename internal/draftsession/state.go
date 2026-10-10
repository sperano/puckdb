package draftsession

func cloneState(state State) State {
	return State{
		Upstream: clonePicks(state.Upstream), Manual: cloneChanges(state.Manual),
		UpstreamPlayers:  clonePlayerSet(state.UpstreamPlayers),
		UpstreamComplete: state.UpstreamComplete, Version: state.Version,
	}
}

func clonePlayerSet(source map[int]bool) map[int]bool {
	cloned := make(map[int]bool, len(source))
	for playerID := range source {
		cloned[playerID] = true
	}
	return cloned
}

// RecordUpstreamPlayers adds every current Yahoo pick and every manual
// change's base (the Yahoo pick it replaced) to state.UpstreamPlayers,
// allocating the set when needed. Reconcile calls it for each observation;
// decoders call it to seed sessions persisted before the set existed.
func RecordUpstreamPlayers(state *State) {
	if state.UpstreamPlayers == nil {
		state.UpstreamPlayers = make(map[int]bool, len(state.Upstream))
	}
	for _, pick := range state.Upstream {
		state.UpstreamPlayers[pick.PlayerID] = true
	}
	for _, change := range state.Manual {
		if change.Base != nil {
			state.UpstreamPlayers[change.Base.PlayerID] = true
		}
	}
}

// DraftDerivedPlayers returns the players whose roster membership the draft
// board decides: everyone Yahoo has placed in a slot during this session,
// including slots since corrected, undone or reset, and every current manual
// pick. An imported roster row for any other player is pre-draft ownership
// (a keeper) that the board cannot take away. A manual pick counts only while
// the entry exists, so undoing a mistaken manual entry restores a keeper.
func DraftDerivedPlayers(state State) map[int]bool {
	derived := clonePlayerSet(state.UpstreamPlayers)
	for _, pick := range state.Upstream {
		derived[pick.PlayerID] = true
	}
	for _, change := range state.Manual {
		for _, pick := range []*Pick{change.Pick, change.Base} {
			if pick != nil {
				derived[pick.PlayerID] = true
			}
		}
	}
	return derived
}

func clonePicks(source map[PickKey]Pick) map[PickKey]Pick {
	cloned := make(map[PickKey]Pick, len(source))
	for key, pick := range source {
		cloned[key] = Pick{Key: pick.Key, TeamID: pick.TeamID, PlayerID: pick.PlayerID, Cost: cloneInt(pick.Cost)}
	}
	return cloned
}

func cloneChanges(source map[PickKey]ManualChange) map[PickKey]ManualChange {
	cloned := make(map[PickKey]ManualChange, len(source))
	for key, change := range source {
		cloned[key] = ManualChange{
			Kind: change.Kind, Pick: clonePick(change.Pick), Base: clonePick(change.Base), Conflict: change.Conflict,
		}
	}
	return cloned
}

func clonePick(pick *Pick) *Pick {
	if pick == nil {
		return nil
	}
	return &Pick{Key: pick.Key, TeamID: pick.TeamID, PlayerID: pick.PlayerID, Cost: cloneInt(pick.Cost)}
}

func clonePickIfPresent(picks map[PickKey]Pick, key PickKey) *Pick {
	pick, exists := picks[key]
	if !exists {
		return nil
	}
	return clonePick(&pick)
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// equalState ignores UpstreamPlayers, which only grows with Upstream changes.
func equalState(left, right State) bool {
	if left.UpstreamComplete != right.UpstreamComplete || left.Version != right.Version ||
		len(left.Upstream) != len(right.Upstream) || len(left.Manual) != len(right.Manual) {
		return false
	}
	for key, pick := range left.Upstream {
		other, ok := right.Upstream[key]
		if !ok || !equalPick(pick, other) {
			return false
		}
	}
	for key, change := range left.Manual {
		other, ok := right.Manual[key]
		if !ok || !equalChange(change, other) {
			return false
		}
	}
	return true
}

func equalChange(left, right ManualChange) bool {
	return left.Kind == right.Kind && left.Conflict == right.Conflict &&
		equalOptionalPick(left.Pick, right.Pick) && equalOptionalPick(left.Base, right.Base)
}

func equalOptionalPick(left, right *Pick) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return equalPick(*left, *right)
}

func equalPick(left, right Pick) bool {
	return left.Key == right.Key && left.TeamID == right.TeamID && left.PlayerID == right.PlayerID &&
		equalOptionalInt(left.Cost, right.Cost)
}

func equalOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func equalPickAt(picks map[PickKey]Pick, key PickKey, pick Pick) bool {
	current, exists := picks[key]
	return exists && equalPick(current, pick)
}
