package nhl

import (
	"testing"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
)

// scheduleGame builds a ScheduleGame for homePlayoffGames tests.
func scheduleGame(id int64, gameType nhlapi.GameType, state nhlapi.GameState, home, away, date string) nhlapi.ScheduleGame {
	return nhlapi.ScheduleGame{
		ID:        nhlapi.GameID(id),
		GameType:  gameType,
		GameState: state,
		GameDate:  &date,
		HomeTeam:  nhlapi.ScheduleTeam{Abbrev: home},
		AwayTeam:  nhlapi.ScheduleTeam{Abbrev: away},
	}
}

func TestHomePlayoffGames_FiltersToFinalHomePlayoffGames(t *testing.T) {
	t.Parallel()

	schedule := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2025-04-20"),
		scheduleGame(2, nhlapi.GameTypePlayoffs, nhlapi.GameStateOff, "TOR", "MTL", "2025-04-22"),
		// Away playoff game: belongs to MTL's partition, not TOR's.
		scheduleGame(3, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "MTL", "TOR", "2025-04-24"),
		// Regular-season home game: wrong game type.
		scheduleGame(4, nhlapi.GameTypeRegularSeason, nhlapi.GameStateFinal, "TOR", "MTL", "2025-01-10"),
		// Not-final playoff home game: excluded until it finishes.
		scheduleGame(5, nhlapi.GameTypePlayoffs, nhlapi.GameStateLive, "TOR", "MTL", "2025-04-26"),
	}}

	games := homePlayoffGames(schedule, "TOR")

	assert.Len(t, games, 2)
	assert.Equal(t, nhlapi.GameID(1), games[0].ID)
	assert.Equal(t, nhlapi.GameID(2), games[1].ID)
}

// TestScheduleIncomplete pins the cache-staleness predicate: a schedule with
// any non-final game must be refetched (mid-season, mid-playoffs), while a
// fully final schedule is complete and cacheable forever. This replaced the
// IsCurrentSeason calendar gate, which froze mid-playoff caches once
// nhl.Current() rolled over on July 1.
func TestScheduleIncomplete(t *testing.T) {
	t.Parallel()

	complete := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypeRegularSeason, nhlapi.GameStateOff, "TOR", "MTL", "2026-01-10"),
		scheduleGame(2, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2026-04-20"),
	}}
	assert.False(t, scheduleIncomplete(complete))

	midPlayoffs := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2026-04-20"),
		scheduleGame(2, nhlapi.GameTypePlayoffs, nhlapi.GameStateFuture, "TOR", "MTL", "2026-04-22"),
	}}
	assert.True(t, scheduleIncomplete(midPlayoffs))

	assert.False(t, scheduleIncomplete(&nhlapi.TeamScheduleResponse{}))
}

// TestHomePlayoffGames_PartitionsSeriesAcrossTeams pins the dedup invariant the
// per-team playoff activities rely on: both teams in a series see every game in
// their schedules, but the home-game filter assigns each game to exactly one team.
func TestHomePlayoffGames_PartitionsSeriesAcrossTeams(t *testing.T) {
	t.Parallel()

	// A five-game series in the 2-2-1 home/away format, as it appears
	// identically in both teams' club schedules.
	series := []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2025-04-20"),
		scheduleGame(2, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2025-04-22"),
		scheduleGame(3, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "MTL", "TOR", "2025-04-24"),
		scheduleGame(4, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "MTL", "TOR", "2025-04-26"),
		scheduleGame(5, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2025-04-28"),
	}
	schedule := &nhlapi.TeamScheduleResponse{Games: series}

	seen := make(map[nhlapi.GameID]int)
	for _, team := range []string{"TOR", "MTL"} {
		for _, g := range homePlayoffGames(schedule, team) {
			seen[g.ID]++
		}
	}

	assert.Len(t, seen, len(series))
	for id, count := range seen {
		assert.Equal(t, 1, count, "game %d assigned to more than one team", id)
	}
}
