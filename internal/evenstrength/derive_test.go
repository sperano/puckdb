package evenstrength

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	homeClub, awayClub     = 1, 2
	h1, h2, h3, h4         = 11, 12, 13, 14
	a1, a2, a3             = 21, 22, 23
	homeGoalie, awayGoalie = 31, 32
)

func skater(player, team int64, period int32, start, end string) Shift {
	return Shift{PlayerID: player, TeamID: team, Period: period, Start: start, End: end, IsSkater: true}
}

func goalie(player, team int64, period int32, start, end string) Shift {
	return Shift{PlayerID: player, TeamID: team, Period: period, Start: start, End: end}
}

func situation(code int32) *int32 { return &code }

// threeOnThree is one minute of 3v3 in period 1; the home club changes h3
// for h4 at 30 seconds.
func threeOnThree() []Shift {
	return []Shift{
		skater(h1, homeClub, 1, "00:00", "01:00"),
		skater(h2, homeClub, 1, "00:00", "01:00"),
		skater(h3, homeClub, 1, "00:00", "00:30"),
		skater(h4, homeClub, 1, "00:30", "01:00"),
		goalie(homeGoalie, homeClub, 1, "00:00", "01:00"),
		skater(a1, awayClub, 1, "00:00", "01:00"),
		skater(a2, awayClub, 1, "00:00", "01:00"),
		skater(a3, awayClub, 1, "00:00", "01:00"),
		goalie(awayGoalie, awayClub, 1, "00:00", "01:00"),
	}
}

func TestDerive_SplitsSegmentsAtLineChanges(t *testing.T) {
	got := Derive(threeOnThree(), nil)

	both := func(a, b int64, seconds int32) []PairTOI {
		return []PairTOI{{a, b, seconds}, {b, a, seconds}}
	}
	var want []PairTOI
	for _, pairs := range [][]PairTOI{
		both(h1, h2, 60), both(h1, h3, 30), both(h1, h4, 30), both(h2, h3, 30), both(h2, h4, 30),
		both(a1, a2, 60), both(a1, a3, 60), both(a2, a3, 60),
	} {
		want = append(want, pairs...)
	}
	assert.ElementsMatch(t, want, got.Pairs)
	assert.Equal(t, []SkaterGame{
		{h1, homeClub, 60, 0}, {h2, homeClub, 60, 0}, {h3, homeClub, 30, 0}, {h4, homeClub, 30, 0},
		{a1, awayClub, 60, 0}, {a2, awayClub, 60, 0}, {a3, awayClub, 60, 0},
	}, got.Skaters, "sorted by player")
}

func TestDerive_CreditsGoalToTheSegmentItEnds(t *testing.T) {
	goals := []Goal{
		{Period: 1, Clock: "00:30", Situation: situation(1331), Credited: []int64{h3, h1}},
		{Period: 1, Clock: "00:30", Situation: situation(1331), Credited: []int64{h4}}, // h4 starts at 30
		{Period: 1, Clock: "00:00", Situation: situation(1331), Credited: []int64{h2}}, // nothing ends at 0
		{Period: 1, Clock: "01:00", Situation: situation(1331), Credited: []int64{a1, a1}},
		{Period: 2, Clock: "00:30", Situation: situation(1331), Credited: []int64{h2}}, // no stint in period 2
		{Period: 1, Clock: "00:45", Situation: situation(1341), Credited: []int64{h2}}, // power play code
		{Period: 1, Clock: "00:45", Credited: []int64{h2}},                             // unknown situation
		{Period: 1, Clock: "0:5", Situation: situation(1331), Credited: []int64{h2}},   // unparsable clock
	}
	points := map[int64]int32{}
	for _, row := range Derive(threeOnThree(), goals).Skaters {
		points[row.PlayerID] = row.Points
	}
	assert.Equal(t, map[int64]int32{h1: 1, h2: 0, h3: 1, h4: 0, a1: 2, a2: 0, a3: 0}, points)
}

func TestDerive_SkipsSegmentsThatAreNotEvenStrength(t *testing.T) {
	cases := map[string][]Shift{
		"empty net": {
			skater(h1, homeClub, 1, "00:00", "01:00"), skater(h2, homeClub, 1, "00:00", "01:00"),
			skater(h3, homeClub, 1, "00:00", "01:00"),
			skater(a1, awayClub, 1, "00:00", "01:00"), skater(a2, awayClub, 1, "00:00", "01:00"),
			skater(a3, awayClub, 1, "00:00", "01:00"), goalie(awayGoalie, awayClub, 1, "00:00", "01:00"),
		},
		"power play": append(threeOnThree()[:4:4],
			goalie(homeGoalie, homeClub, 1, "00:00", "01:00"),
			skater(a1, awayClub, 1, "00:00", "01:00"), skater(a2, awayClub, 1, "00:00", "01:00"),
			goalie(awayGoalie, awayClub, 1, "00:00", "01:00")),
		"2v2": {
			skater(h1, homeClub, 1, "00:00", "01:00"), skater(h2, homeClub, 1, "00:00", "01:00"),
			goalie(homeGoalie, homeClub, 1, "00:00", "01:00"),
			skater(a1, awayClub, 1, "00:00", "01:00"), skater(a2, awayClub, 1, "00:00", "01:00"),
			goalie(awayGoalie, awayClub, 1, "00:00", "01:00"),
		},
		"one club": threeOnThree()[:5],
	}
	for name, shifts := range cases {
		t.Run(name, func(t *testing.T) {
			got := Derive(shifts, nil)
			assert.Empty(t, got.Pairs)
			assert.Empty(t, got.Skaters)
		})
	}
}

func TestDerive_IgnoresUnusableShifts(t *testing.T) {
	shifts := append(threeOnThree(),
		skater(h1, homeClub, 1, "00:00", "01:00"), // duplicate
		skater(h3, homeClub, 1, "00:40", "00:40"), // zero length
		skater(h3, homeClub, 1, "00:50", "00:40"), // reversed
		skater(h3, homeClub, 1, "bad", "00:50"),
		skater(h3, homeClub, 1, "", "00:50"),
		skater(h3, homeClub, 1, "00:40", "100:00"),
	)
	assert.Equal(t, Derive(threeOnThree(), nil), Derive(shifts, nil))
}

func TestParseClock(t *testing.T) {
	valid := map[string]int32{"00:00": 0, "0:59": 59, "05:00": 300, "19:59": 1199, "99:59": 5999}
	for clock, want := range valid {
		got, ok := parseClock(clock)
		require.True(t, ok, clock)
		assert.Equal(t, want, got, clock)
	}
	for _, clock := range []string{"", "bad", "1:5", "100:00", "00:60", "00:00 ", " 00:00", "-1:00", "00:00\n"} {
		_, ok := parseClock(clock)
		assert.False(t, ok, "%q", clock)
	}
}

func TestIsEvenStrengthSituation(t *testing.T) {
	even := []int32{1551, 1441, 1331}
	notEven := []int32{1541, 1451, 651, 1560, 1010, 101, 1661, 1221, 2551, 1552, 0}
	for _, code := range even {
		assert.True(t, isEvenStrengthSituation(code), code)
	}
	for _, code := range notEven {
		assert.False(t, isEvenStrengthSituation(code), code)
	}
}
