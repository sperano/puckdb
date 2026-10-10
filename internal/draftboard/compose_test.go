package draftboard

import (
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	rosterTestLeague   = "500.l.5621"
	rosterTestOurTeam  = 3
	rosterTestOpponent = 8
	keeperPlayerID     = 101
	draftedPlayerID    = 102
	replacementID      = 103
)

var (
	ourPickKey  = draftsession.PickKey{Round: 1, Pick: 2}
	rosterSlots = []draft.RosterSlot{{Position: draft.PositionCenter, Count: 3, Starting: true}}
)

func TestImportedDraftedPlayerLeavesRosterAfterManualUndo(t *testing.T) {
	state := reconciled(t, draftsession.State{}, observedPick(ourPickKey, rosterTestOurTeam, draftedPlayerID))
	require.Equal(t, []int{draftedPlayerID}, rosterIDs(rosterOf(state, importedRoster(draftedPlayerID),
		boardRanking(rosterTestLeague), rosterSlots, rosterTestOurTeam)))

	undone := applied(t, state, draftsession.ManualOperation{Kind: draftsession.ManualUndo, Key: ourPickKey})
	roster := rosterOf(undone, importedRoster(draftedPlayerID), boardRanking(rosterTestLeague), rosterSlots, rosterTestOurTeam)

	assert.Empty(t, draftsession.EffectiveBoard(undone))
	assert.Empty(t, roster.Players, "an undone pick frees its roster spot even though the import still lists it")
	assert.Contains(t, availableIDs(undone, roster), draftedPlayerID)
}

func TestImportedDraftedPlayerLeavesRosterAfterCorrectionToOpponent(t *testing.T) {
	state := reconciled(t, draftsession.State{}, observedPick(ourPickKey, rosterTestOurTeam, draftedPlayerID))
	corrected := applied(t, state, draftsession.ManualOperation{Kind: draftsession.ManualCorrect, Key: ourPickKey,
		Pick: &draftsession.Pick{Key: ourPickKey, TeamID: rosterTestOpponent, PlayerID: draftedPlayerID}})

	roster := rosterOf(corrected, importedRoster(draftedPlayerID), boardRanking(rosterTestLeague), rosterSlots, rosterTestOurTeam)

	assert.Empty(t, roster.Players)
	assert.NotContains(t, availableIDs(corrected, roster), draftedPlayerID, "the opponent now holds the player")
}

func TestStaleImportFollowsLaterYahooReconciliation(t *testing.T) {
	drafted := reconciled(t, draftsession.State{}, observedPick(ourPickKey, rosterTestOurTeam, draftedPlayerID))
	corrected := reconciled(t, drafted, observedPick(ourPickKey, rosterTestOurTeam, replacementID))
	movedToOpponent := reconciled(t, drafted, observedPick(ourPickKey, rosterTestOpponent, draftedPlayerID))
	reset := reconciled(t, drafted)

	for name, test := range map[string]struct {
		state draftsession.State
		want  []int
	}{
		"correction to another player": {state: corrected, want: []int{replacementID}},
		"correction to an opponent":    {state: movedToOpponent, want: nil},
		"authoritative reset":          {state: reset, want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			roster := rosterOf(test.state, importedRoster(draftedPlayerID), boardRanking(rosterTestLeague),
				rosterSlots, rosterTestOurTeam)
			assert.Equal(t, test.want, rosterIDs(roster), "the roster import predates the reconciliation")
		})
	}
}

func TestKeeperStaysThroughUndoAndCorrectionOfDraftedPicks(t *testing.T) {
	imported := importedRoster(keeperPlayerID, draftedPlayerID)
	state := reconciled(t, draftsession.State{}, observedPick(ourPickKey, rosterTestOurTeam, draftedPlayerID))
	undone := applied(t, state, draftsession.ManualOperation{Kind: draftsession.ManualUndo, Key: ourPickKey})
	corrected := applied(t, state, draftsession.ManualOperation{Kind: draftsession.ManualCorrect, Key: ourPickKey,
		Pick: &draftsession.Pick{Key: ourPickKey, TeamID: rosterTestOpponent, PlayerID: draftedPlayerID}})
	reset := reconciled(t, state)

	for name, current := range map[string]draftsession.State{
		"no draft": {}, "drafted": state, "undo": undone, "correction": corrected, "reset": reset,
	} {
		t.Run(name, func(t *testing.T) {
			roster := rosterOf(current, imported, boardRanking(rosterTestLeague), rosterSlots, rosterTestOurTeam)
			assert.Contains(t, rosterIDs(roster), keeperPlayerID)
			assert.NotContains(t, availableIDs(current, roster), keeperPlayerID)
		})
	}
}

func TestKeeperEnteredManuallyForOpponentReturnsWhenUndone(t *testing.T) {
	mistakeKey := draftsession.PickKey{Round: 1, Pick: 1}
	mistake := applied(t, draftsession.State{}, draftsession.ManualOperation{Kind: draftsession.ManualAdd, Key: mistakeKey,
		Pick: &draftsession.Pick{Key: mistakeKey, TeamID: rosterTestOpponent, PlayerID: keeperPlayerID}})
	imported := importedRoster(keeperPlayerID)

	assert.Empty(t, rosterOf(mistake, imported, boardRanking(rosterTestLeague), rosterSlots, rosterTestOurTeam).Players,
		"while the manual entry stands, the board decides the player's team")
	undone := applied(t, mistake, draftsession.ManualOperation{Kind: draftsession.ManualUndo, Key: mistakeKey})
	assert.Equal(t, []int{keeperPlayerID},
		rosterIDs(rosterOf(undone, imported, boardRanking(rosterTestLeague), rosterSlots, rosterTestOurTeam)))
}

func TestImportedKeeperGetsRankingNameAndEligibility(t *testing.T) {
	imported := []draft.RosterPlayer{{YahooPlayerID: keeperPlayerID, Name: "Yahoo player 101"}}

	roster := rosterOf(draftsession.State{}, imported, boardRanking(rosterTestLeague), rosterSlots, rosterTestOurTeam)

	require.Len(t, roster.Players, 1)
	assert.Equal(t, "Opponent pick", roster.Players[0].Name)
	assert.Equal(t, []string{"C"}, roster.Players[0].EligiblePositions)
}

func importedRoster(playerIDs ...int) []draft.RosterPlayer {
	players := make([]draft.RosterPlayer, 0, len(playerIDs))
	for _, playerID := range playerIDs {
		players = append(players, draft.RosterPlayer{YahooPlayerID: playerID, EligiblePositions: []string{"C"}})
	}
	return players
}

func observedPick(key draftsession.PickKey, teamID, playerID int) draftsession.ObservedPick {
	return draftsession.ObservedPick{Key: key, TeamID: teamID, PlayerID: playerID}
}

// reconciled applies one complete, authoritative Yahoo board.
func reconciled(t *testing.T, state draftsession.State, picks ...draftsession.ObservedPick) draftsession.State {
	t.Helper()
	next, _, err := draftsession.Reconcile(state, draftsession.Snapshot{Picks: picks, Authoritative: true,
		HasExpectedCount: true, ExpectedCount: len(picks), RawCount: len(picks)})
	require.NoError(t, err)
	return next
}

func applied(t *testing.T, state draftsession.State, operation draftsession.ManualOperation) draftsession.State {
	t.Helper()
	next, _, err := draftsession.ApplyManual(state, operation)
	require.NoError(t, err)
	return next
}

func rosterIDs(roster Roster) []int {
	var ids []int
	for _, player := range roster.Players {
		ids = append(ids, player.YahooPlayerID)
	}
	return ids
}

func availableIDs(state draftsession.State, roster Roster) []int {
	players := availablePlayers(boardRanking(rosterTestLeague),
		draftrecommend.Result{Scenario: draftrank.ScenarioBase}, state, roster.Players)
	ids := make([]int, 0, len(players))
	for _, player := range players {
		ids = append(ids, player.YahooPlayerID)
	}
	return ids
}
