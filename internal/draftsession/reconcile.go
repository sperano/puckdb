package draftsession

import (
	"fmt"
	"sort"
)

// Reconcile applies resolved positive observations. Only an authoritative,
// count-verified, fully resolved snapshot replaces the upstream board. Partial
// observations may add or correct known slots, but never remove a slot.
func Reconcile(current State, snapshot Snapshot) (State, Report, error) {
	if snapshot.RawCount < 0 || snapshot.ExpectedCount < 0 {
		return current, Report{}, fmt.Errorf("%w: negative result count", ErrInvalidSnapshot)
	}

	next := cloneState(current)
	// Record the prior picks first: a complete snapshot replaces Upstream.
	RecordUpstreamPlayers(&next)
	observed, skipped := validatePicks(snapshot.Picks)
	complete := snapshot.Authoritative && snapshot.HasExpectedCount &&
		snapshot.ExpectedCount == snapshot.RawCount && snapshot.RawCount == len(snapshot.Picks) && len(skipped) == 0
	if snapshot.Authoritative {
		applyObserved(&next, observed, complete)
	}
	// Then the picks this observation added or corrected.
	RecordUpstreamPlayers(&next)
	// Even a non-authoritative response is evidence that this poll did not
	// establish a complete board. Keep the prior picks, but prevent consumers
	// from treating stale state as recommendation-safe.
	next.UpstreamComplete = complete
	resolveManual(&next, complete)
	flagDuplicateConflicts(&next)

	changed := !equalState(current, next)
	if changed {
		next.Version = current.Version + 1
	}
	report := makeReport(next, changed, skipped, countApplied(observed, current, next), countRemoved(current, next))
	return next, report, nil
}

func validatePicks(rows []ObservedPick) (map[PickKey]Pick, []SkippedPick) {
	valid := make(map[PickKey]Pick, len(rows))
	players := make(map[int]PickKey, len(rows))
	var skipped []SkippedPick
	for i, row := range rows {
		if reason := rowIssue(row); reason != "" {
			skipped = append(skipped, SkippedPick{Index: i, Key: row.Key, Reason: reason})
			continue
		}
		if _, exists := valid[row.Key]; exists {
			skipped = append(skipped, SkippedPick{Index: i, Key: row.Key, Reason: "duplicate pick slot"})
			continue
		}
		if prior, exists := players[row.PlayerID]; exists {
			skipped = append(skipped, SkippedPick{Index: i, Key: row.Key,
				Reason: fmt.Sprintf("player %d also appears at round %d pick %d", row.PlayerID, prior.Round, prior.Pick)})
			continue
		}
		valid[row.Key] = Pick{Key: row.Key, TeamID: row.TeamID, PlayerID: row.PlayerID, Cost: cloneInt(row.Cost)}
		players[row.PlayerID] = row.Key
	}
	return valid, skipped
}

func rowIssue(row ObservedPick) string {
	if row.Issue != "" {
		return row.Issue
	}
	if row.Key.Round <= 0 || row.Key.Pick <= 0 {
		return "round and pick must be positive"
	}
	if row.TeamID <= 0 {
		return "team ID is missing or invalid"
	}
	if row.PlayerID <= 0 {
		return "player ID is missing or unmapped"
	}
	return ""
}

func applyObserved(state *State, observed map[PickKey]Pick, complete bool) {
	if complete {
		state.Upstream = clonePicks(observed)
		return
	}
	for key, pick := range observed {
		state.Upstream[key] = pick
	}
}

func resolveManual(state *State, complete bool) {
	for key, change := range state.Manual {
		upstream, exists := state.Upstream[key]
		if manualMatches(change, upstream, exists) {
			delete(state.Manual, key)
			continue
		}
		if complete && !sameAsBase(change.Base, upstream, exists) {
			change.Conflict = true
		} else if complete {
			change.Conflict = false
		}
		state.Manual[key] = change
	}
}

func manualMatches(change ManualChange, upstream Pick, exists bool) bool {
	if change.Kind == ManualUndo {
		return !exists
	}
	return exists && change.Pick != nil && equalPick(*change.Pick, upstream)
}

func sameAsBase(base *Pick, upstream Pick, exists bool) bool {
	if base == nil {
		return !exists
	}
	return exists && equalPick(*base, upstream)
}

func makeReport(state State, changed bool, skipped []SkippedPick, applied, removed int) Report {
	conflicts := make([]PickKey, 0)
	for key, change := range state.Manual {
		if change.Conflict {
			conflicts = append(conflicts, key)
		}
	}
	sortKeys(conflicts)
	return Report{
		Changed: changed, Complete: state.UpstreamComplete,
		SafeToRecommend: SafeToRecommend(state), Applied: applied, Removed: removed,
		Skipped: skipped, Conflicts: conflicts, Duplicates: DuplicatePlayers(state),
	}
}

// SafeToRecommend reports whether the upstream board is complete, all manual
// changes have an unambiguous relationship to Yahoo's latest board, and no
// player occupies two effective slots.
func SafeToRecommend(state State) bool {
	if !state.UpstreamComplete {
		return false
	}
	for _, change := range state.Manual {
		if change.Conflict {
			return false
		}
	}
	return len(DuplicatePlayers(state)) == 0
}

func countApplied(observed map[PickKey]Pick, before, after State) int {
	count := 0
	for key, pick := range observed {
		if !equalPickAt(before.Upstream, key, pick) && equalPickAt(after.Upstream, key, pick) {
			count++
		}
	}
	return count
}

func countRemoved(before, after State) int {
	count := 0
	for key := range before.Upstream {
		if _, ok := after.Upstream[key]; !ok {
			count++
		}
	}
	return count
}

func sortKeys(keys []PickKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Round == keys[j].Round {
			return keys[i].Pick < keys[j].Pick
		}
		return keys[i].Round < keys[j].Round
	})
}
