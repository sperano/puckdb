package maurice

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/yahoolink"
)

// SystemPrompt returns the system prompt with the current date, season and
// the user's Yahoo teams (if any) injected: the teams of the default season
// (yahoolink.DefaultSeason) as the user's teams, the others only for
// questions about their season (userTeamsPhrase). teams is nil or empty when
// the user manages none, or none in a league the instance allows: the
// prompt then names no team.
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
- Use unicode characters, but only use emojis when absolutely necessary.
- Answer in the language of the user's message. In French, always address the user as "vous" `+
		`(vouvoiement), never "tu", even when the user writes "tu".`,
		time.Now().Format("January 2, 2006"),
		season.ID(), season.StartYear(), season.EndYear())
	if phrase := userTeamsPhrase(teams, int32(season.StartYear())); phrase != "" {
		prompt += "\n\n" + phrase
	}
	return prompt
}

// maxOtherSeasonTeams caps the teams of seasons other than the default one
// the prompt names, newest season first; the rest are counted ("and N
// more"), not named, so the prompt stays bounded however many seasons a
// user has played.
const maxOtherSeasonTeams = 6

// userTeamsPhrase names the teams a user manages, or "" for no teams.
// The teams of the default season (yahoolink.DefaultSeason against
// currentSeason, a Yahoo start year) are the user's teams, "my team" in a
// prompt; the teams of every other season follow on one capped line, to be
// used only when the user asks about that season.
func userTeamsPhrase(teams []UserTeam, currentSeason int32) string {
	if len(teams) == 0 {
		return ""
	}
	seasons := make([]int32, len(teams))
	for i, t := range teams {
		seasons[i] = t.Season
	}
	defaultSeason, ok := yahoolink.DefaultSeason(seasons, currentSeason)
	var own, others []UserTeam
	for _, t := range teams {
		if ok && t.Season == defaultSeason {
			own = append(own, t)
		} else {
			others = append(others, t)
		}
	}
	var lines []string
	if len(own) > 0 {
		lines = append(lines, ownTeamsLine(own, defaultSeason))
	}
	if len(others) > 0 {
		lines = append(lines, otherSeasonTeamsLine(others))
	}
	return strings.Join(lines, "\n")
}

// ownTeamsLine names the teams of the default season, all of season.
func ownTeamsLine(teams []UserTeam, season int32) string {
	names := make([]string, len(teams))
	for i, t := range teams {
		names[i] = teamInLeague(t)
	}
	meaning := `"my team" means this team`
	if len(teams) > 1 {
		meaning = `"my team" means one of these teams`
	}
	return fmt.Sprintf("In the %s season the user manages %s; %s.",
		seasonLabel(season), joinWithAnd(names), meaning)
}

// otherSeasonTeamsLine names up to maxOtherSeasonTeams of teams, newest
// season first (league then team within a season, the order teams came
// in), and counts the rest.
func otherSeasonTeamsLine(teams []UserTeam) string {
	sorted := slices.Clone(teams)
	slices.SortStableFunc(sorted, func(a, b UserTeam) int { return cmp.Compare(b.Season, a.Season) })
	shown := sorted[:min(len(sorted), maxOtherSeasonTeams)]
	items := make([]string, len(shown))
	for i, t := range shown {
		items[i] = teamInLeague(t) + ", " + seasonLabel(t.Season)
	}
	line := "The user's teams in other seasons, to mention only when the user asks about that season: " +
		strings.Join(items, "; ")
	if rest := len(sorted) - len(shown); rest > 0 {
		line += fmt.Sprintf("; and %d more", rest)
	}
	return line + "."
}

// teamInLeague formats one team with its league and their IDs.
func teamInLeague(t UserTeam) string {
	return fmt.Sprintf("%s (team %d) in %s (league %d)", t.TeamName, t.TeamID, t.LeagueName, t.LeagueID)
}

// seasonLabel formats a Yahoo season (its start year) as "2026-27".
func seasonLabel(startYear int32) string {
	return fmt.Sprintf("%d-%02d", startYear, (startYear+1)%100)
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
