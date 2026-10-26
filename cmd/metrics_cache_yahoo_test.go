package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/require"
)

// Fixture for the daily team counters: a three-day season with two leagues.
const (
	dailyLeagueA       = 41
	dailyLeagueB       = 42
	dailyLeagueUnknown = 49
	dailyTeamA1        = 1
	dailyTeamA2        = 2
	dailyTeamB3        = 3
	dailyTeamUnknown   = 9
)

func dailyDay(day int) time.Time {
	return time.Date(2024, time.October, day, 0, 0, 0, 0, time.UTC)
}

func dailyFixtureSeason() simpleSeason {
	return simpleSeason{startYear: 2024, start: dailyDay(1), end: dailyDay(3)}
}

func dailyFixtureConfig() config.Season {
	return config.Season{Leagues: []config.League{
		{LeagueID: dailyLeagueA, TeamIDs: []int{dailyTeamA1, dailyTeamA2}},
		{LeagueID: dailyLeagueB, TeamIDs: []int{dailyTeamB3}},
	}}
}

// dailyFile names one cached file of the fixture by league, team and day.
type dailyFile struct {
	leagueID, teamID, day int
}

// dailyFixtureFiles are the fixture's files of the counted type: four inside
// the season (on its first, second and last day) and four that must never be
// counted (the days just outside the season, an unconfigured team and an
// unconfigured league).
var dailyFixtureFiles = []dailyFile{
	{dailyLeagueA, dailyTeamA2, 1},
	{dailyLeagueA, dailyTeamA1, 1},
	{dailyLeagueB, dailyTeamB3, 2},
	{dailyLeagueA, dailyTeamA1, 3},
	{dailyLeagueA, dailyTeamA1, 0},
	{dailyLeagueA, dailyTeamA1, 4},
	{dailyLeagueA, dailyTeamUnknown, 2},
	{dailyLeagueUnknown, dailyTeamA1, 2},
}

// dailyFixtureOtherFile exists only as the other resource type, so it must not
// count for the type under test.
var dailyFixtureOtherFile = dailyFile{dailyLeagueA, dailyTeamA2, 2}

func writeDailyFiles(t *testing.T, storage store.Storage, build dailyTeamResource, files ...dailyFile) {
	t.Helper()
	for _, f := range files {
		r := build(f.leagueID, f.teamID, dailyDay(f.day))
		require.NoError(t, storage.Write(context.Background(), r.Path(), []byte("x")))
	}
}

// TestCountDailyTeamFiles checks, for rosters and team summaries alike, that
// only existing files of the requested type count, for configured teams only,
// on each day from the season start through its end or now (both inclusive).
func TestCountDailyTeamFiles(t *testing.T) {
	t.Parallel()

	types := []struct {
		name         string
		build, other dailyTeamResource
	}{
		{name: "roster", build: rosterResource, other: teamSummaryResource},
		{name: "team summary", build: teamSummaryResource, other: rosterResource},
	}
	cases := []struct {
		name  string
		now   time.Time
		files bool
		cfg   config.Season
		want  int
	}{
		{name: "season over", now: dailyDay(10), files: true, cfg: dailyFixtureConfig(), want: 4},
		{name: "now mid-day", now: dailyDay(2).Add(12 * time.Hour), files: true, cfg: dailyFixtureConfig(), want: 3},
		{name: "now at midnight", now: dailyDay(2), files: true, cfg: dailyFixtureConfig(), want: 3},
		{name: "now on the first day", now: dailyDay(1), files: true, cfg: dailyFixtureConfig(), want: 2},
		{name: "season not started", now: dailyDay(0), files: true, cfg: dailyFixtureConfig(), want: 0},
		{name: "no files", now: dailyDay(10), files: false, cfg: dailyFixtureConfig(), want: 0},
		{name: "no leagues", now: dailyDay(10), files: true, cfg: config.Season{}, want: 0},
	}
	for _, typ := range types {
		for _, tc := range cases {
			t.Run(typ.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				storage := store.NewMemStorage()
				if tc.files {
					writeDailyFiles(t, storage, typ.build, dailyFixtureFiles...)
					writeDailyFiles(t, storage, typ.other, dailyFixtureOtherFile)
				}
				got := countDailyTeamFiles(context.Background(), storage, dailyFixtureSeason(), tc.cfg, tc.now, typ.build)
				require.Equal(t, tc.want, got)
			})
		}
	}
}

// TestDailyTeamResourceBuilders pins each builder to its resource type, so a
// swapped builder cannot count the other type's files.
func TestDailyTeamResourceBuilders(t *testing.T) {
	t.Parallel()

	day := dailyDay(2)
	require.Equal(t,
		resource.Roster{LeagueID: dailyLeagueA, TeamID: dailyTeamA1, Date: day},
		rosterResource(dailyLeagueA, dailyTeamA1, day))
	require.Equal(t,
		resource.TeamSummary{LeagueID: dailyLeagueA, TeamID: dailyTeamA1, Date: day},
		teamSummaryResource(dailyLeagueA, dailyTeamA1, day))
}

// scanFixtureFirstYear is the start year of the first season scanSeasons tests
// feed in; the others follow it year by year.
const scanFixtureFirstYear = 2000
