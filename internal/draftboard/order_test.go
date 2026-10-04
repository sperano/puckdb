package draftboard

import (
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimatedSnakeOrderUsesImportedTeamPositions(t *testing.T) {
	slots := []draft.RosterSlot{
		{Position: draft.PositionCenter, Count: 1},
		{Position: draft.SlotBench, Count: 1},
		{Position: draft.SlotInjuredReserve, Count: 2},
	}
	order := estimatedSnakeOrder(3, slots, []teamDraftPosition{
		{TeamID: 30, Position: 3}, {TeamID: 10, Position: 1}, {TeamID: 20, Position: 2},
	})
	require.Len(t, order, 6)
	assert.Equal(t, []int{10, 20, 30, 30, 20, 10}, []int{
		order[0].TeamID, order[1].TeamID, order[2].TeamID,
		order[3].TeamID, order[4].TeamID, order[5].TeamID,
	})
	assert.Equal(t, 2, order[3].Key.Round)
	assert.Equal(t, 4, order[3].Key.Pick)
}

func TestEstimatedSnakeOrderRejectsIncompletePositions(t *testing.T) {
	slots := []draft.RosterSlot{{Position: draft.PositionCenter, Count: 1}}
	assert.Nil(t, estimatedSnakeOrder(2, slots, []teamDraftPosition{{TeamID: 10, Position: 1}}))
	assert.Nil(t, estimatedSnakeOrder(2, slots, []teamDraftPosition{
		{TeamID: 10, Position: 1}, {TeamID: 20, Position: 1},
	}))
}

func TestEstimatedOrderSupportsYahooLiveAndSnakeFormats(t *testing.T) {
	assert.True(t, supportsEstimatedOrder("live", false))
	assert.True(t, supportsEstimatedOrder("snake", false))
	assert.False(t, supportsEstimatedOrder("live", true))
	assert.False(t, supportsEstimatedOrder("offline", false))
}
