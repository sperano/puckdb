package draftsession

import "fmt"

// ApplyManual applies one explicit local board edit. A change remains marked
// manual until a Yahoo snapshot confirms it or the user resolves a conflict.
// An add or correction whose player already occupies another effective slot
// fails with ErrDuplicatePlayer.
func ApplyManual(current State, operation ManualOperation) (State, Report, error) {
	if !validKey(operation.Key) {
		return current, Report{}, fmt.Errorf("%w: round and pick must be positive", ErrInvalidManual)
	}
	if err := rejectManualDuplicate(current, operation); err != nil {
		return current, Report{}, err
	}
	next := cloneState(current)
	upstream, upstreamExists := next.Upstream[operation.Key]
	effective, effectiveExists := effectiveAt(next, operation.Key)
	change, hasChange := next.Manual[operation.Key]

	switch operation.Kind {
	case ManualAdd:
		if effectiveExists || operation.Pick == nil || !validManualPick(*operation.Pick, operation.Key) {
			return current, Report{}, fmt.Errorf("%w: add requires an empty slot and a valid pick", ErrInvalidManual)
		}
		change = ManualChange{Kind: ManualAdd, Pick: clonePick(operation.Pick)}
		if upstreamExists {
			change.Base = clonePick(&upstream)
		}
	case ManualCorrect:
		if !effectiveExists || operation.Pick == nil || !validManualPick(*operation.Pick, operation.Key) {
			return current, Report{}, fmt.Errorf("%w: correction requires an occupied slot and a valid pick", ErrInvalidManual)
		}
		if equalPick(effective, *operation.Pick) {
			return finishManualTransition(current, next)
		}
		change = ManualChange{Kind: ManualCorrect, Pick: clonePick(operation.Pick)}
		change.Base = clonePickIfPresent(current.Upstream, operation.Key)
	case ManualUndo:
		if !effectiveExists {
			return current, Report{}, fmt.Errorf("%w: cannot undo an empty slot", ErrInvalidManual)
		}
		if hasChange && change.Kind == ManualAdd && !upstreamExists {
			delete(next.Manual, operation.Key)
			break
		}
		change = ManualChange{Kind: ManualUndo, Base: clonePickIfPresent(current.Upstream, operation.Key)}
	default:
		return current, Report{}, fmt.Errorf("%w: unknown operation %q", ErrInvalidManual, operation.Kind)
	}
	if operation.Kind != ManualUndo || upstreamExists || !hasChange || change.Kind != ManualAdd {
		next.Manual[operation.Key] = change
	}
	return finishManualTransition(current, next)
}

// ResolveConflict records the user's explicit choice for one contested slot.
// Keeping a manual pick whose player still occupies another effective slot
// fails with ErrDuplicatePlayer; accepting Yahoo's pick is always allowed, and
// any manual entry it duplicates becomes a conflict.
func ResolveConflict(current State, key PickKey, choice ConflictChoice) (State, Report, error) {
	change, exists := current.Manual[key]
	if !exists || !change.Conflict {
		return current, Report{}, fmt.Errorf("%w: slot has no unresolved conflict", ErrInvalidManual)
	}
	next := cloneState(current)
	switch choice {
	case KeepManual:
		change.Base = clonePickIfPresent(next.Upstream, key)
		change.Conflict = false
		next.Manual[key] = change
		if pick, exists := effectiveAt(next, key); exists {
			if err := rejectDuplicatePlayer(next, key, pick.PlayerID); err != nil {
				return current, Report{}, err
			}
		}
	case AcceptUpstream:
		delete(next.Manual, key)
	default:
		return current, Report{}, fmt.Errorf("%w: unknown conflict choice %q", ErrInvalidManual, choice)
	}
	return finishManualTransition(current, next)
}

func finishManualTransition(before, after State) (State, Report, error) {
	flagDuplicateConflicts(&after)
	changed := !equalState(before, after)
	if changed {
		after.Version = before.Version + 1
	}
	report := makeReport(after, changed, nil, 0, 0)
	return after, report, nil
}

// EffectiveBoard returns picks in slot order with manual entries visibly
// distinguished. Undo tombstones hide the corresponding Yahoo pick.
func EffectiveBoard(state State) []BoardEntry {
	keys := make([]PickKey, 0, len(state.Upstream)+len(state.Manual))
	seen := make(map[PickKey]bool, len(state.Upstream)+len(state.Manual))
	for key := range state.Upstream {
		keys = append(keys, key)
		seen[key] = true
	}
	for key := range state.Manual {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sortKeys(keys)
	board := make([]BoardEntry, 0, len(keys))
	for _, key := range keys {
		pick, exists := effectiveAt(state, key)
		if !exists {
			continue
		}
		source := SourceYahoo
		if _, isManual := state.Manual[key]; isManual {
			source = SourceManual
		}
		board = append(board, BoardEntry{Pick: pick, Source: source})
	}
	return board
}

func effectiveAt(state State, key PickKey) (Pick, bool) {
	if change, exists := state.Manual[key]; exists {
		if change.Kind == ManualUndo || change.Pick == nil {
			return Pick{}, false
		}
		return *clonePick(change.Pick), true
	}
	pick, exists := state.Upstream[key]
	return pick, exists
}

func rejectManualDuplicate(state State, operation ManualOperation) error {
	if operation.Kind == ManualUndo || operation.Pick == nil {
		return nil
	}
	return rejectDuplicatePlayer(state, operation.Key, operation.Pick.PlayerID)
}

func validManualPick(pick Pick, key PickKey) bool {
	return pick.Key == key && pick.TeamID > 0 && pick.PlayerID > 0
}

func validKey(key PickKey) bool { return key.Round > 0 && key.Pick > 0 }
