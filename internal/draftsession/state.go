package draftsession

func cloneState(state State) State {
	return State{
		Upstream: clonePicks(state.Upstream), Manual: cloneChanges(state.Manual),
		UpstreamComplete: state.UpstreamComplete, Version: state.Version,
	}
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
