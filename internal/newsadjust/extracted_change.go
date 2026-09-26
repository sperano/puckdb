package newsadjust

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/sperano/puckdb/internal/newsevent"
)

// Team is one NHL team's names, for resolving a reported destination.
type Team struct {
	ID         int64
	Abbrev     string
	FullName   string
	CommonName string
}

// TeamDirectory resolves a reported team name (abbreviation, full or
// common name, in any case and punctuation) to an NHL team ID. A name two
// teams share resolves to neither.
type TeamDirectory map[string]int64

// ambiguousTeam marks a name more than one team answers to.
const ambiguousTeam int64 = -1

// NewTeamDirectory indexes every team by its normalized names.
func NewTeamDirectory(teams []Team) TeamDirectory {
	d := make(TeamDirectory)
	for _, t := range teams {
		for _, name := range []string{t.Abbrev, t.FullName, t.CommonName} {
			key := normalizeText(name)
			if key == "" {
				continue
			}
			if id, exists := d[key]; exists && id != t.ID {
				d[key] = ambiguousTeam
				continue
			}
			d[key] = t.ID
		}
	}
	return d
}

// Resolve returns the team a name designates.
func (d TeamDirectory) Resolve(name string) (int64, bool) {
	id, found := d[normalizeText(name)]
	return id, found && id != ambiguousTeam
}

// normalizeText lowercases, turns punctuation into spaces and drops a
// leading "the", so "the St. Louis Blues" and "St Louis Blues" match.
func normalizeText(s string) string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(fields) > 0 && fields[0] == "the" {
		fields = fields[1:]
	}
	return strings.Join(fields, " ")
}

// containsPhrase reports whether normalized text contains one of the
// phrases as whole words.
func containsPhrase(text string, phrases []string) bool {
	padded := " " + text + " "
	return slices.ContainsFunc(phrases, func(p string) bool { return strings.Contains(padded, " "+p+" ") })
}

// move is what a reported change of league means for NHL availability.
type move int

const (
	moveUnknown move = iota
	// moveAssignment: sent to a league other than the NHL.
	moveAssignment
	// moveRecall: back in the NHL.
	moveRecall
)

// nhlLeague and otherLeagues are the league names a reported league change
// is read from.
var (
	nhlLeague    = []string{"nhl"}
	otherLeagues = []string{
		"ahl", "echl", "khl", "shl", "liiga", "nl", "del", "ncaa", "chl", "ohl", "whl", "qmjhl", "usports",
		"europe", "juniors", "junior", "minors", "minor league", "minor leagues",
	}
)

func leagueMove(to string) move {
	text := normalizeText(to)
	switch {
	case containsPhrase(text, otherLeagues):
		return moveAssignment
	case containsPhrase(text, nhlLeague):
		return moveRecall
	}
	return moveUnknown
}

// Role phrases, matched as whole words of the normalized new role. Only
// these phrases have a numeric mapping; any other stated role alerts.
var (
	goalieBackupPhrases  = []string{"backup", "back up", "number two", "no 2"}
	goalieTandemPhrases  = []string{"tandem", "1a", "1b", "platoon", "timeshare", "time share", "split starts"}
	goalieStarterPhrases = []string{
		"starter", "starting goaltender", "starting goalie", "starting netminder", "starting job", "starting role",
		"number one goaltender", "number one goalie", "no 1 goaltender", "no 1 goalie",
	}
	powerPlayPhrases     = []string{"power play", "powerplay", "pp", "pp1", "pp2", "man advantage"}
	powerPlayDownPhrases = []string{"second", "2nd", "pp2", "unit 2", "off", "removed", "dropped", "demoted"}
	powerPlayUpPhrases   = []string{"top", "first", "1st", "pp1", "unit 1", "quarterback", "quarterbacking"}
	iceTimeUpPhrases     = []string{
		"top line", "first line", "1st line", "top six", "top 6", "top pair", "top pairing", "first pair",
		"first pairing", "1st pair", "top minutes",
	}
	iceTimeDownPhrases = []string{
		"fourth line", "4th line", "bottom six", "bottom 6", "third pair", "third pairing", "3rd pair",
		"bottom pair", "bottom pairing", "reduced minutes",
	}
)

// roleFromText reads the goalie role, power-play and ice-time direction a
// stated new role names.
func roleFromText(to string) *RoleChange {
	text := normalizeText(to)
	role := &RoleChange{}
	switch {
	case containsPhrase(text, goalieBackupPhrases):
		role.GoalieRole = GoalieRoleBackup
	case containsPhrase(text, goalieTandemPhrases):
		role.GoalieRole = GoalieRoleTandem
	case containsPhrase(text, goalieStarterPhrases):
		role.GoalieRole = GoalieRoleStarter
	}
	if containsPhrase(text, powerPlayPhrases) {
		switch {
		case containsPhrase(text, powerPlayDownPhrases):
			role.PowerPlay = DirectionDown
		case containsPhrase(text, powerPlayUpPhrases):
			role.PowerPlay = DirectionUp
		}
	}
	switch {
	case containsPhrase(text, iceTimeDownPhrases):
		role.IceTime = DirectionDown
	case containsPhrase(text, iceTimeUpPhrases):
		role.IceTime = DirectionUp
	}
	return role
}

// mapChange maps a trade or role change by the field it moves. It returns
// a hold reason when the stated change has no numeric mapping.
func (c *converter) mapChange(ev *Event, src *ExtractedEvent) string {
	change := src.Event.Change
	switch change.Field {
	case newsevent.ChangeTeam:
		ev.Type = EventTrade
		team, found := c.extraction.Teams.Resolve(change.To)
		if !found {
			return fmt.Sprintf("trade destination %q matches no single NHL team", change.To)
		}
		ev.Role = &RoleChange{TeamID: &team}
		return mapDuration(ev, src.Event.Duration)
	case newsevent.ChangeLeague:
		return c.mapLeagueMove(ev, src)
	case newsevent.ChangeRole:
		ev.Type = EventRoleChange
		ev.Role = roleFromText(change.To)
		if ev.Role.empty() {
			return fmt.Sprintf("role %q has no numeric mapping", change.To)
		}
		return mapDuration(ev, src.Event.Duration)
	default:
		ev.Type = EventRoleChange
		return fmt.Sprintf("%s change to %q has no numeric mapping; injuries and suspensions carry availability",
			fieldLabel(change.Field), change.To)
	}
}

func fieldLabel(field newsevent.ChangeField) string {
	if field == newsevent.ChangeNone {
		return "unspecified"
	}
	return strings.ReplaceAll(string(field), "_", " ")
}

// mapLeagueMove makes an assignment out of the NHL an absence and a recall
// a return that ends the player's earlier assignments.
func (c *converter) mapLeagueMove(ev *Event, src *ExtractedEvent) string {
	switch leagueMove(src.Event.Change.To) {
	case moveAssignment:
		ev.Type = EventAbsence
		return mapDuration(ev, src.Event.Duration)
	case moveRecall:
		ev.Type = EventReturn
		ev.Supersedes = c.assignmentsBefore(ev)
		if len(ev.Supersedes) == 0 {
			return "the recall ends no assignment on record"
		}
		return ""
	default:
		ev.Type = EventRoleChange
		return fmt.Sprintf("league change to %q has no numeric mapping", src.Event.Change.To)
	}
}

// assignmentsBefore lists the player's assignments that took effect before
// the recall.
func (c *converter) assignmentsBefore(recall *Event) []string {
	var ids []string
	for _, a := range c.assignments[recall.PlayerKey] {
		if id := extractedID(a.ID); id != recall.ID && assignmentStart(a).Before(recall.start()) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// assignmentStart is when an assignment took effect: its stated date, else
// its earliest report.
func assignmentStart(a *ExtractedEvent) time.Time {
	if !a.Event.EffectiveOn.IsZero() {
		return a.Event.EffectiveOn.UTC()
	}
	var earliest time.Time
	for _, ev := range a.Evidence {
		if ev.Relation == newsevent.RelationSupports && (earliest.IsZero() || ev.ReportedAt.Before(earliest)) {
			earliest = ev.ReportedAt
		}
	}
	return earliest.UTC()
}
