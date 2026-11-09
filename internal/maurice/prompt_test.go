package maurice

import (
	"fmt"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
)

func TestSystemPrompt_NoTeamsNamesNone(t *testing.T) {
	prompt := SystemPrompt(nil)
	assert.Contains(t, prompt, "Maurice")
	assert.NotContains(t, prompt, "manages")
}

func TestSystemPrompt_NamesOneTeam(t *testing.T) {
	prompt := SystemPrompt([]UserTeam{{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny"}})
	assert.Contains(t, prompt, "The user manages Les Peter Stastny (team 8, league 29249).")
}

func TestSystemPrompt_NamesSeveralTeams(t *testing.T) {
	prompt := SystemPrompt([]UserTeam{
		{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny"},
		{LeagueID: 1001, TeamID: 3, TeamName: "Second Pool"},
	})
	assert.Contains(t, prompt, "Les Peter Stastny (team 8, league 29249) and Second Pool (team 3, league 1001).")
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
