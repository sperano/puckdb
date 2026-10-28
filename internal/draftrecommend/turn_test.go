package draftrecommend

import (
	"testing"

	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	turnTeamA = 10
	turnTeamB = 20
	turnTeamC = 30
	// turnTeamOutside owns no slot of the order.
	turnTeamOutside = 99
	turnRounds      = 3
	turnPlayerBase  = 1000
)

// turnSnakeOrder is the snake order of teams over rounds: picks are numbered
// overall, and even rounds run the teams in reverse.
func turnSnakeOrder(teams []int, rounds int) []PickSlot {
	order := make([]PickSlot, 0, len(teams)*rounds)
	for round := 1; round <= rounds; round++ {
		for offset := range teams {
			position := offset
			if round%2 == 0 {
				position = len(teams) - offset - 1
			}
			pick := (round-1)*len(teams) + offset + 1
			order = append(order, PickSlot{Key: draftsession.PickKey{Round: round, Pick: pick}, TeamID: teams[position]})
		}
	}
	return order
}

// turnState fills the given overall picks of order with Yahoo picks.
func turnState(order []PickSlot, picks ...int) draftsession.State {
	state := draftsession.State{Upstream: map[draftsession.PickKey]draftsession.Pick{}}
	for _, pick := range picks {
		slot := order[pick-1]
		state.Upstream[slot.Key] = draftsession.Pick{Key: slot.Key, TeamID: slot.TeamID, PlayerID: turnPlayerBase + pick}
	}
	return state
}

func turnKey(round, pick int) *draftsession.PickKey {
	return &draftsession.PickKey{Round: round, Pick: pick}
}

func TestNextTurn(t *testing.T) {
	// Picks 1-9 belong to A B C | C B A | A B C.
	snake := turnSnakeOrder([]int{turnTeamA, turnTeamB, turnTeamC}, turnRounds)
	undone := turnState(snake, 1)
	undone.Manual = map[draftsession.PickKey]draftsession.ManualChange{
		snake[0].Key: {Kind: draftsession.ManualUndo},
	}
	manualAdd := turnState(snake)
	manualAdd.Manual = map[draftsession.PickKey]draftsession.ManualChange{
		snake[0].Key: {Kind: draftsession.ManualAdd, Pick: &draftsession.Pick{
			Key: snake[0].Key, TeamID: turnTeamA, PlayerID: turnPlayerBase,
		}},
	}
	custom := []PickSlot{snake[1], snake[0], snake[2]}

	tests := []struct {
		name      string
		order     []PickSlot
		state     draftsession.State
		teamID    int
		wantKey   *draftsession.PickKey
		wantTeam  int
		wantUntil *int
	}{
		{name: "first pick on the clock", order: snake, state: turnState(snake), teamID: turnTeamA,
			wantKey: turnKey(1, 1), wantTeam: turnTeamA, wantUntil: new(0)},
		{name: "odd round waits for earlier teams", order: snake, state: turnState(snake), teamID: turnTeamC,
			wantKey: turnKey(1, 1), wantTeam: turnTeamA, wantUntil: new(2)},
		{name: "even round runs in reverse", order: snake, state: turnState(snake, 1, 2, 3), teamID: turnTeamA,
			wantKey: turnKey(2, 4), wantTeam: turnTeamC, wantUntil: new(2)},
		{name: "even round current pick", order: snake, state: turnState(snake, 1, 2, 3, 4), teamID: turnTeamB,
			wantKey: turnKey(2, 5), wantTeam: turnTeamB, wantUntil: new(0)},
		{name: "even round wait crosses into odd round", order: snake, state: turnState(snake, 1, 2, 3, 4),
			teamID: turnTeamC, wantKey: turnKey(2, 5), wantTeam: turnTeamB, wantUntil: new(4)},
		{name: "same team ends odd round", order: snake, state: turnState(snake, 1, 2), teamID: turnTeamC,
			wantKey: turnKey(1, 3), wantTeam: turnTeamC, wantUntil: new(0)},
		{name: "same team starts even round", order: snake, state: turnState(snake, 1, 2, 3), teamID: turnTeamC,
			wantKey: turnKey(2, 4), wantTeam: turnTeamC, wantUntil: new(0)},
		{name: "same team ends even round", order: snake, state: turnState(snake, 1, 2, 3, 4, 5), teamID: turnTeamA,
			wantKey: turnKey(2, 6), wantTeam: turnTeamA, wantUntil: new(0)},
		{name: "same team starts odd round", order: snake, state: turnState(snake, 1, 2, 3, 4, 5, 6),
			teamID: turnTeamA, wantKey: turnKey(3, 7), wantTeam: turnTeamA, wantUntil: new(0)},
		{name: "filled later picks are skipped", order: snake, state: turnState(snake, 1, 3), teamID: turnTeamC,
			wantKey: turnKey(1, 2), wantTeam: turnTeamB, wantUntil: new(1)},
		{name: "manual undo reopens a Yahoo pick", order: snake, state: undone, teamID: turnTeamB,
			wantKey: turnKey(1, 1), wantTeam: turnTeamA, wantUntil: new(1)},
		{name: "manual add fills a slot", order: snake, state: manualAdd, teamID: turnTeamB,
			wantKey: turnKey(1, 2), wantTeam: turnTeamB, wantUntil: new(0)},
		{name: "slice order wins over pick numbers", order: custom, state: turnState(snake), teamID: turnTeamA,
			wantKey: turnKey(1, 2), wantTeam: turnTeamB, wantUntil: new(1)},
		{name: "team owns no remaining slot", order: snake, state: turnState(snake, 1, 2, 3, 4, 5, 6, 7),
			teamID: turnTeamA, wantKey: turnKey(3, 8), wantTeam: turnTeamB},
		{name: "team outside the order", order: snake, state: turnState(snake), teamID: turnTeamOutside,
			wantKey: turnKey(1, 1), wantTeam: turnTeamA},
		{name: "every pick filled", order: snake, state: turnState(snake, 1, 2, 3, 4, 5, 6, 7, 8, 9),
			teamID: turnTeamA},
		{name: "empty order", state: turnState(snake), teamID: turnTeamA},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			current, until := NextTurn(tc.order, tc.state, tc.teamID)
			if tc.wantKey == nil {
				assert.Nil(t, current)
			} else {
				require.NotNil(t, current)
				assert.Equal(t, PickSlot{Key: *tc.wantKey, TeamID: tc.wantTeam}, *current)
			}
			assert.Equal(t, tc.wantUntil, until)
		})
	}
}

func TestNextTurnReturnsACopyOfTheSlot(t *testing.T) {
	order := turnSnakeOrder([]int{turnTeamA, turnTeamB}, turnRounds)
	current, _ := NextTurn(order, draftsession.State{}, turnTeamA)
	require.NotNil(t, current)
	current.TeamID = turnTeamOutside
	assert.Equal(t, turnTeamA, order[0].TeamID)
}
