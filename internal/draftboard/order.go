package draftboard

import (
	"strings"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
)

const (
	standardDraftTypeSnake = "snake"
	standardDraftTypeLive  = "live"
)

type teamDraftPosition struct {
	TeamID   int
	Position int
}

func supportsEstimatedOrder(format string, auction bool) bool {
	normalized := strings.ToLower(format)
	return !auction && (strings.Contains(normalized, standardDraftTypeSnake) ||
		strings.Contains(normalized, standardDraftTypeLive))
}

func estimatedSnakeOrder(numTeams int, slots []draft.RosterSlot, teams []teamDraftPosition) []draftrecommend.PickSlot {
	teamIDs, ok := teamsByDraftPosition(numTeams, teams)
	if !ok {
		return nil
	}
	rounds := draftRoundCount(slots)
	order := make([]draftrecommend.PickSlot, 0, rounds*numTeams)
	for round := 1; round <= rounds; round++ {
		for offset := 0; offset < numTeams; offset++ {
			position := offset
			if round%2 == 0 {
				position = numTeams - offset - 1
			}
			pick := (round-1)*numTeams + offset + 1
			order = append(order, draftrecommend.PickSlot{
				Key: draftsession.PickKey{Round: round, Pick: pick}, TeamID: teamIDs[position],
			})
		}
	}
	return order
}

func teamsByDraftPosition(numTeams int, teams []teamDraftPosition) ([]int, bool) {
	if numTeams <= 0 || len(teams) != numTeams {
		return nil, false
	}
	teamIDs := make([]int, numTeams)
	for _, team := range teams {
		if team.TeamID <= 0 || team.Position <= 0 || team.Position > numTeams || teamIDs[team.Position-1] != 0 {
			return nil, false
		}
		teamIDs[team.Position-1] = team.TeamID
	}
	return teamIDs, true
}

func draftRoundCount(slots []draft.RosterSlot) int {
	rounds := 0
	for _, slot := range slots {
		if slot.Count > 0 && slot.Kind() != draft.SlotKindReserve && slot.Kind() != draft.SlotKindUnsupported {
			rounds += slot.Count
		}
	}
	return rounds
}
