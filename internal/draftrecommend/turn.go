package draftrecommend

import "github.com/sperano/puckdb/internal/draftsession"

// NextTurn walks order, in slice order, past the slots the session's
// effective board (Yahoo picks with manual changes applied) has filled. It
// returns a copy of the first unfilled slot, the pick on the clock, and the
// number of unfilled slots before teamID's next unfilled one: 0 when teamID
// is on the clock. Both are nil when every slot is filled; the count alone is
// nil when teamID owns no remaining slot. NextTurn does not validate order
// (positive, unique keys) nor check that every filled pick is in it.
func NextTurn(order []PickSlot, state draftsession.State, teamID int) (*PickSlot, *int) {
	board := draftsession.EffectiveBoard(state)
	filled := make(map[draftsession.PickKey]bool, len(board))
	for _, entry := range board {
		filled[entry.Pick.Key] = true
	}
	return nextTurn(order, filled, teamID)
}

func nextTurn(order []PickSlot, filled map[draftsession.PickKey]bool, teamID int) (*PickSlot, *int) {
	var current *PickSlot
	pending := 0
	for _, slot := range order {
		if filled[slot.Key] {
			continue
		}
		if current == nil {
			current = &slot
		}
		if slot.TeamID == teamID {
			return current, &pending
		}
		pending++
	}
	return current, nil
}
