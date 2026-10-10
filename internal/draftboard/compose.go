package draftboard

import (
	"fmt"
	"slices"
	"sort"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
)

func picksOf(state draftsession.State, ranking *draftrank.Snapshot) []Pick {
	players := make(map[int]draftrank.Player, len(ranking.Players))
	for _, player := range ranking.Players {
		players[player.YahooPlayerID] = player
	}
	entries := draftsession.EffectiveBoard(state)
	picks := make([]Pick, 0, len(entries))
	for _, entry := range entries {
		player, mapped := players[entry.Pick.PlayerID]
		pick := Pick{Round: entry.Pick.Key.Round, Pick: entry.Pick.Key.Pick,
			TeamID: entry.Pick.TeamID, PlayerID: entry.Pick.PlayerID, Source: entry.Source,
			Cost: cloneInt(entry.Pick.Cost)}
		if change, exists := state.Manual[entry.Pick.Key]; exists {
			pick.Conflict = change.Conflict
		}
		if mapped {
			pick.PlayerKey, pick.Name = player.PlayerKey, player.Name
		} else {
			pick.Name = fmt.Sprintf("Yahoo player %d", entry.Pick.PlayerID)
		}
		picks = append(picks, pick)
	}
	for key, change := range state.Manual {
		if change.Kind != draftsession.ManualUndo || !change.Conflict {
			continue
		}
		pick, exists := state.Upstream[key]
		if !exists && change.Base != nil {
			pick = *change.Base
		}
		player, mapped := players[pick.PlayerID]
		row := Pick{Round: key.Round, Pick: key.Pick, TeamID: pick.TeamID,
			PlayerID: pick.PlayerID, Source: draftsession.SourceManual, Conflict: true, Undone: true,
			Cost: cloneInt(pick.Cost), Name: fmt.Sprintf("Yahoo player %d", pick.PlayerID)}
		if mapped {
			row.PlayerKey, row.Name = player.PlayerKey, player.Name
		}
		picks = append(picks, row)
	}
	sort.Slice(picks, func(left, right int) bool { return picks[left].Pick < picks[right].Pick })
	return picks
}

// rosterOf lists our team: imported players the draft session has never
// placed (keepers and other pre-draft ownership), then every effective pick
// for our team. An imported player the session has placed is draft-derived,
// so its membership follows the effective board: an undo, a correction to
// another team, or a later Yahoo correction removes it even while the roster
// import still lists it.
func rosterOf(state draftsession.State, imported []draft.RosterPlayer, ranking *draftrank.Snapshot, slots []draft.RosterSlot, teamID int) Roster {
	players := make([]draft.RosterPlayer, 0, len(imported)+len(state.Upstream))
	draftDerived := draftsession.DraftDerivedPlayers(state)
	for _, player := range imported {
		if draftDerived[player.YahooPlayerID] {
			continue
		}
		players = appendRosterPlayer(players, withRankingDetails(player, ranking))
	}
	seen := make(map[int]bool, len(players))
	for _, player := range players {
		seen[player.YahooPlayerID] = true
	}
	for _, entry := range draftsession.EffectiveBoard(state) {
		if entry.Pick.TeamID != teamID {
			continue
		}
		if !seen[entry.Pick.PlayerID] {
			player, _ := rankingPlayerByID(ranking, entry.Pick.PlayerID)
			players = append(players, draft.RosterPlayer{YahooPlayerID: entry.Pick.PlayerID,
				Name: player.Name, EligiblePositions: slices.Clone(player.RosterEligiblePositions)})
			seen[entry.Pick.PlayerID] = true
		}
	}
	feasibility := draft.CheckRoster(slots, players)
	return Roster{Players: players, Assignments: feasibility.Assignments,
		OpenSlots: feasibility.OpenSlots, Warnings: feasibility.Warnings(), Feasible: feasibility.Feasible()}
}

// withRankingDetails fills an imported player's missing name and eligibility
// from the ranking snapshot.
func withRankingDetails(player draft.RosterPlayer, ranking *draftrank.Snapshot) draft.RosterPlayer {
	ranked, exists := rankingPlayerByID(ranking, player.YahooPlayerID)
	if !exists {
		return player
	}
	if player.Name == "" || player.Name == fmt.Sprintf("Yahoo player %d", player.YahooPlayerID) {
		player.Name = ranked.Name
	}
	if len(player.EligiblePositions) == 0 {
		player.EligiblePositions = slices.Clone(ranked.RosterEligiblePositions)
	}
	return player
}

func appendRosterPlayer(players []draft.RosterPlayer, player draft.RosterPlayer) []draft.RosterPlayer {
	for index := range players {
		if players[index].YahooPlayerID == player.YahooPlayerID {
			players[index] = player
			return players
		}
	}
	return append(players, player)
}

func rankingPlayerByID(ranking *draftrank.Snapshot, playerID int) (draftrank.Player, bool) {
	for _, player := range ranking.Players {
		if player.YahooPlayerID == playerID {
			return player, true
		}
	}
	return draftrank.Player{}, false
}

func availablePlayers(snapshot *draftrank.Snapshot, recommendation draftrecommend.Result,
	state draftsession.State, roster []draft.RosterPlayer) []Player {
	selectedScenario := recommendation.Scenario
	drafted := make(map[int]bool, len(roster)+len(state.Upstream))
	for _, player := range roster {
		drafted[player.YahooPlayerID] = true
	}
	for _, entry := range draftsession.EffectiveBoard(state) {
		drafted[entry.Pick.PlayerID] = true
	}
	recommended := make(map[string]draftrecommend.Candidate, len(recommendation.Candidates))
	for _, candidate := range recommendation.Candidates {
		recommended[candidate.PlayerKey] = candidate
	}
	players := make([]Player, 0, len(snapshot.Players))
	for _, player := range snapshot.Players {
		if player.YahooPlayerID <= 0 || drafted[player.YahooPlayerID] {
			continue
		}
		baseline, hasBaseline := player.Placements[draftrank.ScenarioBaseline]
		selected, hasSelected := player.Placements[selectedScenario]
		if !hasSelected || !hasBaseline {
			continue
		}
		available := Player{PlayerKey: player.PlayerKey, YahooPlayerID: player.YahooPlayerID,
			Name: player.Name, Team: player.Team, Positions: slices.Clone(player.EligiblePositions),
			Status: player.Status, InjuryNote: player.InjuryNote,
			BaselineRank: baseline.OverallRank, SelectedRank: selected.OverallRank,
			BaselineValue: baseline.AdjustedValue, SelectedValue: selected.AdjustedValue,
			News: newsOf(player)}
		if candidate, exists := recommended[player.PlayerKey]; exists {
			candidateCopy := candidate
			available.Recommendation = &candidateCopy
		}
		players = append(players, available)
	}
	return players
}

func newsOf(player draftrank.Player) []NewsSummary {
	if player.Adjustment == nil {
		return nil
	}
	news := make([]NewsSummary, 0, len(player.Adjustment.Reasons))
	for _, reason := range player.Adjustment.Reasons {
		scenarios := make([]string, len(reason.Scenarios))
		for index, scenario := range reason.Scenarios {
			scenarios[index] = string(scenario)
		}
		news = append(news, NewsSummary{Detail: reason.Detail, EffectiveFrom: reason.EffectiveFrom,
			LatestEvidenceAt: reason.LatestEvidenceAt, Scenarios: scenarios, Status: string(reason.Status)})
	}
	return news
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func clonePickSlot(slot *draftrecommend.PickSlot) *draftrecommend.PickSlot {
	if slot == nil {
		return nil
	}
	copy := *slot
	return &copy
}
