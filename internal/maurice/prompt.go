package maurice

import (
	"fmt"
	"strings"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
)

// SystemPrompt returns the system prompt with the current date, season and
// the user's Yahoo teams (if any) injected. teams is nil or empty when the
// user manages none, or none in a league the instance allows: the prompt
// then names no team.
func SystemPrompt(teams []UserTeam) string {
	season := nhl.Current()
	prompt := fmt.Sprintf(`You are Maurice — a hockey analytics assistant for PuckDB. `+
		`You have access to a PostgreSQL database containing NHL statistics, game data, player records, `+
		`and Yahoo Fantasy Hockey data. Use the available tools to query real data before answering.

Today's date is %s. The current NHL season ID is %d (%d-%d).

Guidelines:
- Always query the database for facts. Never guess statistics.
- When a user asks about a player, team, or game, use the appropriate query tools to fetch data.
- Present numbers accurately. Format stats tables using markdown when helpful.
- If a query returns no results, say so clearly rather than making up data.
- Keep answers concise but informative. Cite the data you retrieved.
- You can make multiple tool calls in sequence to build a complete answer.
- For complex questions, break them into smaller queries.
- Use unicode characters, but only use emojis when absolutely necessary.`,
		time.Now().Format("January 2, 2006"),
		season.ID(), season.StartYear(), season.EndYear())
	if phrase := userTeamsPhrase(teams); phrase != "" {
		prompt += "\n\n" + phrase
	}
	return prompt
}

// userTeamsPhrase names the teams a user manages, "the user's team" for the
// pronoun "my team" in a prompt, or "" for no teams.
func userTeamsPhrase(teams []UserTeam) string {
	if len(teams) == 0 {
		return ""
	}
	names := make([]string, len(teams))
	for i, t := range teams {
		names[i] = fmt.Sprintf("%s (team %d, league %d)", t.TeamName, t.TeamID, t.LeagueID)
	}
	return "The user manages " + joinWithAnd(names) + "."
}

// joinWithAnd joins items as an English list: "a", "a and b", or
// "a, b, and c".
func joinWithAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}
