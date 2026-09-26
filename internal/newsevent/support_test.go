package newsevent

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStatesCount(t *testing.T) {
	tests := []struct {
		name  string
		quote string
		n     int
		units []string
		want  bool
	}{
		{"digits", "suspended for 5 games", 5, []string{"game", "games"}, true},
		{"spelled hyphenated", "handed a five-game suspension", 5, []string{"game", "games"}, true},
		{"compound spelled", "banned twenty-five games for the incident", 25, []string{"game", "games"}, true},
		{"unit mismatch", "missed 5 practices this week", 5, []string{"game", "games"}, false},
		{"article is not a count of one", "hurt in a game last night", 1, []string{"game", "games"}, false},
		{"number not followed by a unit", "scored 5 goals in the game", 5, []string{"game", "games"}, false},
		{"qualified unit", "suspended for three preseason and regular-season games", 3, []string{"game", "games"}, true},
		{"another number before the unit", "missed 2 of the last 5 games", 2, []string{"game", "games"}, false},
		{"unit too far", "suspended 3 more very long and tedious games", 3, []string{"game", "games"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, statesCount(tt.quote, tt.n, tt.units...))
		})
	}
}

func TestNumberAt(t *testing.T) {
	tests := []struct {
		name         string
		words        []string
		i            int
		wantN, wantW int
	}{
		{"digit", []string{"5", "games"}, 0, 5, 1},
		{"spelled", []string{"five", "games"}, 0, 5, 1},
		{"compound", []string{"twenty", "five", "games"}, 0, 25, 2},
		{"tens word alone at end of words", []string{"out", "for", "twenty"}, 2, 20, 1},
		{"tens followed by a non-unit word", []string{"twenty", "dollars"}, 0, 20, 1},
		{"tens followed by another tens word", []string{"twenty", "thirty"}, 0, 20, 1},
		{"not a number at all", []string{"suspended", "games"}, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, w := numberAt(tt.words, tt.i)
			assert.Equal(t, tt.wantN, n, "value")
			assert.Equal(t, tt.wantW, w, "width")
		})
	}
}

func TestStatesDate(t *testing.T) {
	reported := time.Date(2026, time.October, 6, 20, 0, 0, 0, time.UTC)
	oct7 := time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)

	t.Run("month and day, dotted abbreviation", func(t *testing.T) {
		assert.True(t, statesDate("The suspension begins Oct. 7", oct7, reported))
	})
	t.Run("day before month", func(t *testing.T) {
		assert.True(t, statesDate("he returns 7 October", oct7, reported))
	})
	t.Run("ordinal suffix", func(t *testing.T) {
		assert.True(t, statesDate("effective Oct. 7th", oct7, reported))
	})
	t.Run("ISO date embedded in prose", func(t *testing.T) {
		assert.True(t, statesDate("league records list the date as 2026-10-07 on file", oct7, reported))
	})
	t.Run("relative today within tolerance", func(t *testing.T) {
		// reported's calendar day is one day behind date's UTC day start, right
		// at the 24h boundary the comment on dayTolerance allows for.
		assert.True(t, statesDate("effective immediately", oct7, reported))
	})
	t.Run("relative today outside tolerance", func(t *testing.T) {
		tooFar := time.Date(2026, time.October, 8, 0, 0, 0, 0, time.UTC)
		assert.False(t, statesDate("effective immediately", tooFar, reported))
	})
	t.Run("relative tomorrow", func(t *testing.T) {
		assert.True(t, statesDate("he is expected back tomorrow", oct7, reported))
	})
	t.Run("weekday within reach", func(t *testing.T) {
		date := reported.AddDate(0, 0, 3)
		word := strings.ToLower(date.Weekday().String())
		assert.True(t, statesDate("back in the lineup "+word, date, reported))
	})
	t.Run("weekday too far away", func(t *testing.T) {
		date := reported.AddDate(0, 0, 14)
		word := strings.ToLower(date.Weekday().String())
		assert.False(t, statesDate("back in the lineup "+word, date, reported))
	})
	t.Run("wrong weekday", func(t *testing.T) {
		date := reported.AddDate(0, 0, 3)
		wrong := strings.ToLower(date.AddDate(0, 0, 1).Weekday().String())
		assert.False(t, statesDate("back in the lineup "+wrong, date, reported))
	})
	t.Run("no date stated at all", func(t *testing.T) {
		assert.False(t, statesDate("he is expected back soon", oct7, reported))
	})
}

func TestStatesKind(t *testing.T) {
	tests := []struct {
		name  string
		quote string
		kind  DurationKind
		want  bool
	}{
		{"indefinite - indefinitely", "suspended indefinitely by the team", DurationIndefinite, true},
		{"indefinite - until further notice", "out until further notice", DurationIndefinite, true},
		{"indefinite - no timetable", "the club gave no timetable for his return", DurationIndefinite, true},
		{"indefinite wrong kind", "suspended indefinitely by the team", DurationSeason, false},
		{"day to day", "considered day to day with a lower-body injury", DurationDayToDay, true},
		{"day to day wrong kind", "considered day to day with a lower-body injury", DurationWeekToWeek, false},
		{"week to week", "listed as week to week", DurationWeekToWeek, true},
		{"month to month", "considered month to month at this point", DurationMonthToMonth, true},
		{"season - rest of the season", "out for the rest of the season", DurationSeason, true},
		{"season - out for the year", "he is out for the year", DurationSeason, true},
		{"season wrong kind", "out for the rest of the season", DurationIndefinite, false},
		{"unknown kind has no phrases", "day to day", DurationUnknown, false},
		{"games kind has no phrases", "suspended for five games", DurationGames, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, statesKind(tt.quote, tt.kind))
		})
	}
}

func TestContainsPhrase(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		phrase string
		want   bool
	}{
		{"accents stripped", "Le club a annoncé que Salo était suspendu pour cinq matchs.", "etait suspendu", true},
		{"curly quote matches straight quote", "The team said it won’t comment further.", "won't comment further", true},
		{"punctuation and dash differences", "Sabres, forward Dmitri Salo—suspended for five games.", "sabres forward dmitri salo suspended", true},
		{"phrase not present", "the team made no comment at all today.", "won't comment further", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, containsPhrase(tt.text, tt.phrase))
		})
	}
}
