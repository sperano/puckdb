package maurice

import (
	"fmt"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
)

// testCurrentSeason is the current Yahoo season (start year) the
// userTeamsPhrase tests pass, so they do not depend on today's date.
const testCurrentSeason = 2026

func TestSystemPrompt_NoTeamsNamesNone(t *testing.T) {
	prompt := SystemPrompt(nil)
	assert.Contains(t, prompt, "Maurice")
	assert.NotContains(t, prompt, "manages")
	assert.NotContains(t, prompt, "other seasons")
	assert.Equal(t, "", userTeamsPhrase(nil, testCurrentSeason))
}

func TestSystemPrompt_NamesTeamOfCurrentNHLSeason(t *testing.T) {
	season := int32(nhl.Current().StartYear())
	prompt := SystemPrompt([]UserTeam{
		{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", LeagueName: "Crapettes", Season: season},
	})
	assert.Contains(t, prompt, fmt.Sprintf(
		`In the %s season the user manages Les Peter Stastny (team 8) in Crapettes (league 29249); "my team" means this team.`,
		seasonLabel(season)))
}

// TestSystemPrompt_FutureSeasonTeamIsNeverTheUsers: SystemPrompt passes the
// current NHL season's start year, so a team one season later goes on the
// other-seasons line, not "my team".
func TestSystemPrompt_FutureSeasonTeamIsNeverTheUsers(t *testing.T) {
	season := int32(nhl.Current().StartYear())
	prompt := SystemPrompt([]UserTeam{
		{LeagueID: 700, TeamID: 1, TeamName: "Next Year", LeagueName: "Future", Season: season + 1},
		{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", LeagueName: "Crapettes", Season: season},
	})
	assert.Contains(t, prompt, fmt.Sprintf(
		`In the %s season the user manages Les Peter Stastny (team 8) in Crapettes (league 29249); "my team" means this team.`+
			"\nThe user's teams in other seasons, to mention only when the user asks about that season: "+
			"Next Year (team 1) in Future (league 700), %s.",
		seasonLabel(season), seasonLabel(season+1)))
}

func TestUserTeamsPhrase_CurrentSeasonOnly(t *testing.T) {
	got := userTeamsPhrase([]UserTeam{
		{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", LeagueName: "Crapettes", Season: 2026},
		{LeagueID: 1001, TeamID: 3, TeamName: "Second Pool", LeagueName: "Office", Season: 2026},
	}, testCurrentSeason)
	assert.Equal(t, `In the 2026-27 season the user manages Les Peter Stastny (team 8) in Crapettes (league 29249)`+
		` and Second Pool (team 3) in Office (league 1001); "my team" means one of these teams.`, got)
}

func TestUserTeamsPhrase_CurrentAndPastSeasons(t *testing.T) {
	got := userTeamsPhrase([]UserTeam{
		{LeagueID: 500, TeamID: 2, TeamName: "Old Guard", LeagueName: "Vintage", Season: 2024},
		{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", LeagueName: "Crapettes", Season: 2026},
		{LeagueID: 600, TeamID: 4, TeamName: "Last Year", LeagueName: "Crapettes", Season: 2025},
	}, testCurrentSeason)
	assert.Equal(t, `In the 2026-27 season the user manages Les Peter Stastny (team 8) in Crapettes (league 29249); "my team" means this team.`+
		"\nThe user's teams in other seasons, to mention only when the user asks about that season: "+
		"Last Year (team 4) in Crapettes (league 600), 2025-26; Old Guard (team 2) in Vintage (league 500), 2024-25.", got)
}

// TestUserTeamsPhrase_OffSeasonFallsBackToNewestPastSeason: before the new
// season's leagues are imported, the newest earlier season is "my team"; a
// season after the current one is never the default.
func TestUserTeamsPhrase_OffSeasonFallsBackToNewestPastSeason(t *testing.T) {
	got := userTeamsPhrase([]UserTeam{
		{LeagueID: 500, TeamID: 2, TeamName: "Old Guard", LeagueName: "Vintage", Season: 2024},
		{LeagueID: 600, TeamID: 4, TeamName: "Last Year", LeagueName: "Crapettes", Season: 2025},
		{LeagueID: 700, TeamID: 1, TeamName: "Next Year", LeagueName: "Future", Season: 2027},
	}, testCurrentSeason)
	assert.Equal(t, `In the 2025-26 season the user manages Last Year (team 4) in Crapettes (league 600); "my team" means this team.`+
		"\nThe user's teams in other seasons, to mention only when the user asks about that season: "+
		"Next Year (team 1) in Future (league 700), 2027-28; Old Guard (team 2) in Vintage (league 500), 2024-25.", got)
}

func TestUserTeamsPhrase_OnlyFutureSeasonsNamesNoOwnTeam(t *testing.T) {
	got := userTeamsPhrase([]UserTeam{
		{LeagueID: 700, TeamID: 1, TeamName: "Next Year", LeagueName: "Future", Season: 2027},
	}, testCurrentSeason)
	assert.Equal(t, "The user's teams in other seasons, to mention only when the user asks about that season: "+
		"Next Year (team 1) in Future (league 700), 2027-28.", got)
}

// TestUserTeamsPhrase_CapsOtherSeasonTeams: past teams beyond
// maxOtherSeasonTeams are counted, not named, keeping the newest seasons
// and, within a season, the league then team order the teams came in.
func TestUserTeamsPhrase_CapsOtherSeasonTeams(t *testing.T) {
	teams := []UserTeam{{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", LeagueName: "Crapettes", Season: 2026}}
	const pastTeams = maxOtherSeasonTeams + 2
	for i := range pastTeams {
		teams = append(teams, UserTeam{
			LeagueID: int32(100 + i), TeamID: 1, TeamName: fmt.Sprintf("T%d", i),
			LeagueName: fmt.Sprintf("L%d", i), Season: int32(2018 + i/2),
		})
	}
	got := userTeamsPhrase(teams, testCurrentSeason)
	assert.Equal(t, `In the 2026-27 season the user manages Les Peter Stastny (team 8) in Crapettes (league 29249); "my team" means this team.`+
		"\nThe user's teams in other seasons, to mention only when the user asks about that season: "+
		"T6 (team 1) in L6 (league 106), 2021-22; T7 (team 1) in L7 (league 107), 2021-22; "+
		"T4 (team 1) in L4 (league 104), 2020-21; T5 (team 1) in L5 (league 105), 2020-21; "+
		"T2 (team 1) in L2 (league 102), 2019-20; T3 (team 1) in L3 (league 103), 2019-20; and 2 more.", got)
}

// TestUserTeamsPhrase_OtherSeasonTeamsCapBoundary: exactly
// maxOtherSeasonTeams other-season teams are all named with no tail; one
// more is counted, not named.
func TestUserTeamsPhrase_OtherSeasonTeamsCapBoundary(t *testing.T) {
	const ownLine = `In the 2026-27 season the user manages Les Peter Stastny (team 8) in Crapettes (league 29249); "my team" means this team.`
	const shownItems = "\nThe user's teams in other seasons, to mention only when the user asks about that season: " +
		"T0 (team 1) in L0 (league 100), 2025-26; T1 (team 1) in L1 (league 101), 2024-25; " +
		"T2 (team 1) in L2 (league 102), 2023-24; T3 (team 1) in L3 (league 103), 2022-23; " +
		"T4 (team 1) in L4 (league 104), 2021-22; T5 (team 1) in L5 (league 105), 2020-21"
	cases := map[string]struct {
		otherTeams int
		want       string
	}{
		"at the cap":   {maxOtherSeasonTeams, ownLine + shownItems + "."},
		"one over cap": {maxOtherSeasonTeams + 1, ownLine + shownItems + "; and 1 more."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			teams := []UserTeam{{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", LeagueName: "Crapettes", Season: testCurrentSeason}}
			for i := range c.otherTeams {
				teams = append(teams, UserTeam{
					LeagueID: int32(100 + i), TeamID: 1, TeamName: fmt.Sprintf("T%d", i),
					LeagueName: fmt.Sprintf("L%d", i), Season: int32(testCurrentSeason - 1 - i),
				})
			}
			assert.Equal(t, c.want, userTeamsPhrase(teams, testCurrentSeason))
		})
	}
}

func TestSeasonLabel(t *testing.T) {
	assert.Equal(t, "2026-27", seasonLabel(2026))
	assert.Equal(t, "1999-00", seasonLabel(1999))
}

func TestJoinWithAnd(t *testing.T) {
	cases := map[string]struct {
		items []string
		want  string
	}{
		"none":  {nil, ""},
		"one":   {[]string{"a"}, "a"},
		"two":   {[]string{"a", "b"}, "a and b"},
		"three": {[]string{"a", "b", "c"}, "a, b, and c"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, joinWithAnd(c.items))
		})
	}
}

func TestSystemPrompt_KeepsDateAndSeasonGuidelines(t *testing.T) {
	prompt := SystemPrompt(nil)
	season := nhl.Current()
	assert.Contains(t, prompt, time.Now().Format("January 2, 2006"))
	assert.Contains(t, prompt, fmt.Sprintf("The current NHL season ID is %d (%d-%d).", season.ID(), season.StartYear(), season.EndYear()))
	assert.Contains(t, prompt, "Guidelines:")
}
