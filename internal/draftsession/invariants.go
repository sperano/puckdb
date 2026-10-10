package draftsession

import "fmt"

// DuplicatePlayers returns every player that occupies more than one effective
// slot, ordered by the player's first slot. A player can be drafted only once,
// so any entry makes the board unsafe for recommendations.
func DuplicatePlayers(state State) []DuplicatePlayer {
	slots := make(map[int][]PickKey)
	var order []int
	for _, entry := range EffectiveBoard(state) {
		playerID := entry.Pick.PlayerID
		if len(slots[playerID]) == 0 {
			order = append(order, playerID)
		}
		slots[playerID] = append(slots[playerID], entry.Pick.Key)
	}
	var duplicates []DuplicatePlayer
	for _, playerID := range order {
		if keys := slots[playerID]; len(keys) > 1 {
			duplicates = append(duplicates, DuplicatePlayer{PlayerID: playerID, Keys: keys})
		}
	}
	return duplicates
}

// rejectDuplicatePlayer fails when playerID occupies an effective slot other
// than key. Moving a player is an explicit sequence: undo or correct the slot
// that holds it, then add or correct the target slot.
func rejectDuplicatePlayer(state State, key PickKey, playerID int) error {
	for _, entry := range EffectiveBoard(state) {
		if entry.Pick.PlayerID == playerID && entry.Pick.Key != key {
			held := entry.Pick.Key
			return fmt.Errorf("%w: player %d is at round %d pick %d; undo or correct that slot first",
				ErrDuplicatePlayer, playerID, held.Round, held.Pick)
		}
	}
	return nil
}

// flagDuplicateConflicts marks every manual entry whose player also occupies
// another effective slot as a conflict, so an upstream arrival that contradicts
// local intent needs an explicit resolution. Flags are only raised here; a
// complete reconcile or an explicit resolution clears them.
func flagDuplicateConflicts(state *State) {
	for _, duplicate := range DuplicatePlayers(*state) {
		for _, key := range duplicate.Keys {
			if change, isManual := state.Manual[key]; isManual && !change.Conflict {
				change.Conflict = true
				state.Manual[key] = change
			}
		}
	}
}
