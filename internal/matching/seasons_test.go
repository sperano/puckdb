package matching

import (
	"errors"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Yahoo IDs and NHL IDs of the namesakes the game player lists leave tied
// once Yahoo Sports has no birth date page for the retired one.
const (
	greenOlderYahooID   store.YahooPlayerID = 2167
	greenYoungerYahooID store.YahooPlayerID = 3667
	greenOlderNHLID                         = 8468436
	greenYoungerNHLID                       = 8471242

	svobodaOlderYahooID   store.YahooPlayerID = 469
	svobodaYoungerYahooID store.YahooPlayerID = 2134
	svobodaOlderNHLID                         = 8451789
	svobodaYoungerNHLID                       = 8467510
)

// seasonRange returns the start years first through last.
func seasonRange(first, last int) []int {
	var seasons []int
	for year := first; year <= last; year++ {
		seasons = append(seasons, year)
	}
	return seasons
}

// seasonsLanding is a landing with no jersey number or birth date (so
// neither step decides) and an NHL regular-season total for each season.
func seasonsLanding(id int64, first, last string, seasons []int) *nhl.PlayerLanding {
	landing := &nhl.PlayerLanding{
		PlayerID:  nhl.PlayerID(id),
		FirstName: nhl.LocalizedString{Default: first},
		LastName:  nhl.LocalizedString{Default: last},
	}
	for _, year := range seasons {
		landing.SeasonTotals = append(landing.SeasonTotals, nhl.SeasonTotal{
			Season: nhl.NewSeason(year), GameType: nhl.GameTypeRegularSeason, LeagueAbbrev: nhlLeagueAbbrev, GamesPlayed: 1,
		})
	}
	return landing
}

// namesakesOf returns a NamesakeSeasons giving sets, counting its calls.
func namesakesOf(calls *int, sets ...[]int) NamesakeSeasons {
	return func() ([][]int, error) {
		*calls++
		return sets, nil
	}
}

func poolOf(players ...store.YahooPlayer) map[store.YahooPlayerID]*store.YahooPlayer {
	pool := make(map[store.YahooPlayerID]*store.YahooPlayer, len(players))
	for i := range players {
		pool[players[i].YahooID] = &players[i]
	}
	return pool
}

// The two Mike Greens: the 1979 one played one NHL season (2003-04), the
// 1985 one 2005-06 to 2019-20. Yahoo lists 2167 in 2001-2003 and 3667 from
// 2005, so each Green's seasons overlap exactly one ID, which the other
// Green's seasons do not.
func greenPool() map[store.YahooPlayerID]*store.YahooPlayer {
	return poolOf(
		store.YahooPlayer{YahooID: greenOlderYahooID, FirstName: "Mike", LastName: "Green", Seasons: seasonRange(2001, 2003)},
		store.YahooPlayer{YahooID: greenYoungerYahooID, FirstName: "Mike", LastName: "Green", Seasons: seasonRange(2005, 2019)},
	)
}

var (
	greenOlderSeasons   = []int{2003}
	greenYoungerSeasons = seasonRange(2005, 2019)
)

func TestMatchYahooIDBySeasons_MikeGreens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		landing   *nhl.PlayerLanding
		namesakes []int
		wantID    store.YahooPlayerID
	}{
		{name: "born 1979", landing: seasonsLanding(greenOlderNHLID, "Mike", "Green", greenOlderSeasons),
			namesakes: greenYoungerSeasons, wantID: greenOlderYahooID},
		{name: "born 1985", landing: seasonsLanding(greenYoungerNHLID, "Mike", "Green", greenYoungerSeasons),
			namesakes: greenOlderSeasons, wantID: greenYoungerYahooID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls int
			got, err := MatchYahooIDBySeasons(tc.landing, "", time.Time{}, namesakesOf(&calls, tc.namesakes), greenPool())

			require.NoError(t, err)
			assert.Equal(t, YahooIDMatchResult{YahooID: tc.wantID, Matched: true, Reason: MatchReasonNameSeasons}, got)
			assert.Equal(t, 1, calls)
		})
	}
}

// The two Petr Svobodas both played 2000-01, the 1966 one's last NHL season
// (1984-85 on) and the 1980 one's only one, and the lists start in 2000, so
// 469 can only be listed in 2000. Whatever seasons the lists give 2134, the
// seasons cannot tell which Svoboda 469 is: both stay unmatched rather than
// one taking the other's ID.
func TestMatchYahooIDBySeasons_PetrSvobodas(t *testing.T) {
	t.Parallel()
	olderSeasons := seasonRange(1984, 2000)
	youngerSeasons := []int{2000}
	listings := []struct {
		name    string
		younger []int // 2134's list seasons
	}{
		{name: "2134 listed in 2000 too", younger: seasonRange(2000, 2002)},
		{name: "2134 listed only after 2000", younger: seasonRange(2001, 2002)},
	}
	players := []struct {
		name      string
		landing   *nhl.PlayerLanding
		namesakes []int
	}{
		{name: "born 1966", landing: seasonsLanding(svobodaOlderNHLID, "Petr", "Svoboda", olderSeasons), namesakes: youngerSeasons},
		{name: "born 1980", landing: seasonsLanding(svobodaYoungerNHLID, "Petr", "Svoboda", youngerSeasons), namesakes: olderSeasons},
	}
	for _, listing := range listings {
		for _, player := range players {
			t.Run(listing.name+"/"+player.name, func(t *testing.T) {
				t.Parallel()
				pool := poolOf(
					store.YahooPlayer{YahooID: svobodaOlderYahooID, FirstName: "Petr", LastName: "Svoboda", Seasons: []int{2000}},
					store.YahooPlayer{YahooID: svobodaYoungerYahooID, FirstName: "Petr", LastName: "Svoboda", Seasons: listing.younger},
				)
				var calls int
				got, err := MatchYahooIDBySeasons(player.landing, "", time.Time{}, namesakesOf(&calls, player.namesakes), pool)

				require.Error(t, err)
				assert.Equal(t, YahooIDMatchResult{Reason: MatchReasonAmbiguous}, got)
			})
		}
	}
}

// The step needs every candidate's list seasons and the player's NHL
// seasons, a single overlapping candidate, and the namesakes' seasons;
// without one of them it decides nothing and the match stays ambiguous.
func TestMatchYahooIDBySeasons_StepDecidesNothing(t *testing.T) {
	t.Parallel()
	errDB := errors.New("database down")
	cases := []struct {
		name      string
		landing   *nhl.PlayerLanding
		pool      map[store.YahooPlayerID]*store.YahooPlayer
		namesakes NamesakeSeasons
		wantCalls int
	}{
		{name: "a candidate has no list seasons",
			landing: seasonsLanding(greenOlderNHLID, "Mike", "Green", greenOlderSeasons),
			pool: poolOf(
				store.YahooPlayer{YahooID: greenOlderYahooID, FirstName: "Mike", LastName: "Green", Seasons: seasonRange(2001, 2003)},
				store.YahooPlayer{YahooID: greenYoungerYahooID, FirstName: "Mike", LastName: "Green"},
			)},
		{name: "the player has no NHL season",
			landing: seasonsLanding(greenOlderNHLID, "Mike", "Green", nil), pool: greenPool()},
		{name: "two candidates overlap",
			landing: seasonsLanding(greenOlderNHLID, "Mike", "Green", []int{2003, 2005}), pool: greenPool()},
		{name: "no candidate overlaps",
			landing: seasonsLanding(greenOlderNHLID, "Mike", "Green", []int{2004}), pool: greenPool()},
		{name: "the namesakes cannot be listed",
			landing:   seasonsLanding(greenOlderNHLID, "Mike", "Green", greenOlderSeasons),
			pool:      greenPool(),
			namesakes: func() ([][]int, error) { return nil, errDB }, wantCalls: 1},
		{name: "a namesake played a season the candidate is listed in",
			landing:   seasonsLanding(greenOlderNHLID, "Mike", "Green", greenOlderSeasons),
			pool:      greenPool(),
			wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls int
			namesakes := tc.namesakes
			if namesakes == nil {
				namesakes = namesakesOf(&calls, greenYoungerSeasons, []int{2002})
			} else {
				inner := namesakes
				namesakes = func() ([][]int, error) { calls++; return inner() }
			}

			got, err := MatchYahooIDBySeasons(tc.landing, "", time.Time{}, namesakes, tc.pool)

			require.Error(t, err)
			assert.Equal(t, YahooIDMatchResult{Reason: MatchReasonAmbiguous}, got)
			assert.Equal(t, tc.wantCalls, calls, "the namesakes are listed only once one candidate overlaps")
		})
	}
}

// Without a namesake lookup, MatchYahooIDBySeasons is MatchYahooID: no
// seasons step.
func TestMatchYahooIDBySeasons_NilNamesakesSkipsTheStep(t *testing.T) {
	t.Parallel()
	landing := seasonsLanding(greenOlderNHLID, "Mike", "Green", greenOlderSeasons)

	got, err := MatchYahooIDBySeasons(landing, "", time.Time{}, nil, greenPool())

	require.Error(t, err)
	assert.Equal(t, YahooIDMatchResult{Reason: MatchReasonAmbiguous}, got)
}

// The step sits between the birth date and team steps: a birth date that
// singles out a candidate decides before it, and it decides before a team
// that would pick the other candidate.
func TestMatchYahooIDBySeasons_StepOrder(t *testing.T) {
	t.Parallel()
	t.Run("birth date first", func(t *testing.T) {
		t.Parallel()
		pool := greenPool()
		birth := date(1985, time.October, 12)
		pool[greenYoungerYahooID].BirthDate = birth
		var calls int

		got, err := MatchYahooIDBySeasons(seasonsLanding(greenOlderNHLID, "Mike", "Green", greenOlderSeasons),
			"", birth, namesakesOf(&calls, greenYoungerSeasons), pool)

		require.NoError(t, err)
		assert.Equal(t, YahooIDMatchResult{YahooID: greenYoungerYahooID, Matched: true, Reason: MatchReasonNameBirthdate}, got)
		assert.Zero(t, calls)
	})
	t.Run("before the team", func(t *testing.T) {
		t.Parallel()
		pool := greenPool()
		pool[greenYoungerYahooID].Team = "Washington"
		var calls int

		got, err := MatchYahooIDBySeasons(seasonsLanding(greenOlderNHLID, "Mike", "Green", greenOlderSeasons),
			"WSH", time.Time{}, namesakesOf(&calls, greenYoungerSeasons), pool)

		require.NoError(t, err)
		assert.Equal(t, YahooIDMatchResult{YahooID: greenOlderYahooID, Matched: true, Reason: MatchReasonNameSeasons}, got)
	})
}

func TestNHLSeasonsPlayed(t *testing.T) {
	t.Parallel()
	total := func(year int, gameType nhl.GameType, league string, games int) nhl.SeasonTotal {
		return nhl.SeasonTotal{Season: nhl.NewSeason(year), GameType: gameType, LeagueAbbrev: league, GamesPlayed: games}
	}
	landing := &nhl.PlayerLanding{SeasonTotals: []nhl.SeasonTotal{
		total(2005, nhl.GameTypeRegularSeason, nhlLeagueAbbrev, 70),
		total(2003, nhl.GameTypeRegularSeason, nhlLeagueAbbrev, 1),
		total(2005, nhl.GameTypePlayoffs, nhlLeagueAbbrev, 4),
		total(2004, nhl.GameTypeRegularSeason, "AHL", 80),
		total(2006, nhl.GameTypePreseason, nhlLeagueAbbrev, 3),
		total(2007, nhl.GameTypeRegularSeason, nhlLeagueAbbrev, 0),
		total(2008, nhl.GameTypePlayoffs, nhlLeagueAbbrev, 2),
	}}

	assert.Equal(t, []int{2003, 2005, 2008}, NHLSeasonsPlayed(landing))
}
