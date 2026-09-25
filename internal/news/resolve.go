package news

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Role says whether a story is about a player or only names them.
type Role string

const (
	RoleSubject   Role = "subject"
	RoleMentioned Role = "mentioned"
)

// Resolution says whether a mention was attached to exactly one player.
type Resolution string

const (
	ResolutionResolved   Resolution = "resolved"
	ResolutionAmbiguous  Resolution = "ambiguous"
	ResolutionUnresolved Resolution = "unresolved"
)

// Method says how a mention was resolved (or why it was not).
type Method string

const (
	// MethodSourceNHLID and MethodSourceYahooID: the source tagged the
	// player by ID.
	MethodSourceNHLID   Method = "source_nhl_id"
	MethodSourceYahooID Method = "source_yahoo_id"
	// MethodBodyNHLID: the story body links the player by NHL ID.
	MethodBodyNHLID Method = "body_nhl_id"
	// MethodName: the full name matches exactly one known player.
	MethodName Method = "name"
	// MethodNameTeam: several players share the name and exactly one plays
	// for a team the story names.
	MethodNameTeam Method = "name_team"
	// MethodTeamConflict: the only player with the name is not on the team
	// the source attached to the story, so it may be someone else (a
	// rookie with the same name).
	MethodTeamConflict Method = "team_conflict"
	// MethodTitlePrefix: the story's "Name: headline" title names a player
	// nobody by that name is known.
	MethodTitlePrefix Method = "title_prefix"
)

const (
	// minNameWords and maxNameWords bound the length of a full name. A
	// surname alone is never matched: it is too often shared.
	minNameWords = 2
	maxNameWords = 4
	// titlePrefixSeparator ends the player name in "Name: headline" titles.
	titlePrefixSeparator = ":"
)

// yahooTeamAbbrevs maps Yahoo's team abbreviations to NHL ones where they
// differ.
var yahooTeamAbbrevs = map[string]string{
	"NJ": "NJD", "TB": "TBL", "LA": "LAK", "SJ": "SJS", "WAS": "WSH", "MON": "MTL",
	"CLS": "CBJ", "NAS": "NSH", "CAL": "CGY", "UTAH": "UTA", "ARI": "UTA",
}

// NHLTeamAbbrev returns the NHL abbreviation of a Yahoo team abbreviation.
func NHLTeamAbbrev(abbrev string) string {
	abbrev = strings.ToUpper(strings.TrimSpace(abbrev))
	if nhl, ok := yahooTeamAbbrevs[abbrev]; ok {
		return nhl
	}
	return abbrev
}

// Identity is one player as known to the NHL/Yahoo mapping. Either ID may be
// zero: a rookie Yahoo lists may have no NHL record yet.
type Identity struct {
	NHLPlayerID   int64  `json:"nhlPlayerId,omitempty"`
	YahooPlayerID int    `json:"yahooPlayerId,omitempty"`
	Name          string `json:"name"`
	Team          string `json:"team,omitempty"`
}

// Key identifies the player, preferring the NHL ID.
func (i Identity) Key() string {
	if i.NHLPlayerID != 0 {
		return fmt.Sprintf("nhl:%d", i.NHLPlayerID)
	}
	return fmt.Sprintf("yahoo:%d", i.YahooPlayerID)
}

// NHLPlayer is a players row as the directory needs it.
type NHLPlayer struct {
	ID        int64
	YahooID   int
	FirstName string
	LastName  string
	Team      string
	Active    bool
}

// YahooPlayer is a Yahoo pool player with its NHL mapping (0 when unmapped).
type YahooPlayer struct {
	YahooPlayerID int
	NHLPlayerID   int64
	Name          string
	Team          string
}

// Team is an NHL team's names.
type Team struct {
	Abbrev     string
	FullName   string
	CommonName string
}

// Directory resolves player names and IDs. Names are matched only for
// active NHL players and players in the Yahoo pools: a retired namesake must
// not capture a story about a rookie.
type Directory struct {
	identities []Identity
	byNHL      map[int64]int
	byYahoo    map[int]int
	byName     map[string][]int
	teams      map[string]string
}

// NewDirectory builds a directory from the NHL players, the Yahoo pool
// players (merged into the NHL player they map to) and the team names.
func NewDirectory(nhlPlayers []NHLPlayer, yahooPlayers []YahooPlayer, teams []Team) *Directory {
	d := &Directory{
		byNHL: make(map[int64]int), byYahoo: make(map[int]int),
		byName: make(map[string][]int), teams: make(map[string]string),
	}
	named := make(map[int][]string)
	for _, p := range nhlPlayers {
		idx := d.add(Identity{NHLPlayerID: p.ID, YahooPlayerID: p.YahooID,
			Name: strings.TrimSpace(p.FirstName + " " + p.LastName), Team: strings.ToUpper(p.Team)})
		if p.Active {
			named[idx] = append(named[idx], d.identities[idx].Name)
		}
	}
	for _, p := range yahooPlayers {
		idx, ok := d.lookup(p.NHLPlayerID, p.YahooPlayerID)
		if !ok {
			idx = d.add(Identity{YahooPlayerID: p.YahooPlayerID, Name: p.Name, Team: NHLTeamAbbrev(p.Team)})
		}
		identity := &d.identities[idx]
		if identity.YahooPlayerID == 0 {
			identity.YahooPlayerID = p.YahooPlayerID
			d.byYahoo[p.YahooPlayerID] = idx
		}
		if identity.Team == "" {
			identity.Team = NHLTeamAbbrev(p.Team)
		}
		named[idx] = append(named[idx], identity.Name, p.Name)
	}
	for idx := range d.identities {
		d.indexNames(idx, named[idx])
	}
	for _, t := range teams {
		for _, name := range []string{t.FullName, t.CommonName} {
			if key := strings.Join(Words(name), " "); key != "" {
				d.teams[key] = strings.ToUpper(t.Abbrev)
			}
		}
	}
	return d
}

func (d *Directory) add(identity Identity) int {
	idx := len(d.identities)
	d.identities = append(d.identities, identity)
	if identity.NHLPlayerID != 0 {
		d.byNHL[identity.NHLPlayerID] = idx
	}
	if identity.YahooPlayerID != 0 {
		d.byYahoo[identity.YahooPlayerID] = idx
	}
	return idx
}

func (d *Directory) lookup(nhlID int64, yahooID int) (int, bool) {
	if idx, ok := d.byNHL[nhlID]; ok && nhlID != 0 {
		return idx, true
	}
	if idx, ok := d.byYahoo[yahooID]; ok && yahooID != 0 {
		return idx, true
	}
	return 0, false
}

func (d *Directory) indexNames(idx int, names []string) {
	for _, name := range names {
		words := Words(name)
		if len(words) < minNameWords || len(words) > maxNameWords {
			continue
		}
		key := strings.Join(words, " ")
		if !slices.Contains(d.byName[key], idx) {
			d.byName[key] = append(d.byName[key], idx)
		}
	}
}

// Story is what resolution reads from an article version.
type Story struct {
	Title     string
	Text      string
	Subjects  []Subject
	TeamHints []string
	// Structured stories (Yahoo status) name their player by ID only; their
	// generated text is not scanned for names.
	Structured bool
}

// Mention is one player reference found in a story.
type Mention struct {
	Text       string
	Role       Role
	Resolution Resolution
	Method     Method
	// Player is set when Resolution is ResolutionResolved.
	Player Identity
	// Candidates lists the players an ambiguous mention could be.
	Candidates []Identity
}

// Resolve finds the players a story names. A name is attached to a player
// only when exactly one known player fits; otherwise the mention is kept as
// ambiguous or unresolved for review.
func (d *Directory) Resolve(story Story) []Mention {
	titleWords := Words(story.Title)
	mentions := d.resolveSubjects(story.Subjects, titleWords)
	if story.Structured {
		return mentions
	}
	known := make(map[string]bool, len(mentions))
	for _, m := range mentions {
		known[m.Player.Key()] = true
	}
	teams := d.storyTeams(story, titleWords)
	seen := make(map[string]bool)
	titleHits := d.findNames(titleWords)
	for _, part := range []struct {
		hits []nameHit
		role Role
	}{{titleHits, RoleSubject}, {d.findNames(Words(story.Text)), RoleMentioned}} {
		for _, hit := range part.hits {
			if seen[hit.name] || d.coversKnown(hit, known) {
				continue
			}
			seen[hit.name] = true
			mentions = append(mentions, d.resolveName(hit, part.role, story.TeamHints, teams))
		}
	}
	if prefix, ok := titlePrefixName(story.Title); ok && !seen[prefix] && !d.prefixKnown(prefix, known) {
		mentions = append(mentions, Mention{
			Text: prefix, Role: RoleSubject, Resolution: ResolutionUnresolved, Method: MethodTitlePrefix,
		})
	}
	return promoteSoleMention(mentions)
}

// promoteSoleMention makes the only player a story names its subject when
// its title names nobody ("Canucks acquire defenceman in trade").
func promoteSoleMention(mentions []Mention) []Mention {
	sole := -1
	for i, m := range mentions {
		switch {
		case m.Role == RoleSubject:
			return mentions
		case sole >= 0:
			return mentions
		default:
			sole = i
		}
	}
	if sole >= 0 && mentions[sole].Resolution == ResolutionResolved {
		mentions[sole].Role = RoleSubject
	}
	return mentions
}

// resolveSubjects resolves the players the source tagged or its body links.
// With several tagged players only those the title names are subjects; a
// player the body links is a subject only when the title names them.
func (d *Directory) resolveSubjects(subjects []Subject, titleWords []string) []Mention {
	tagged := 0
	for _, s := range subjects {
		if !s.InBody {
			tagged++
		}
	}
	mentions := make([]Mention, 0, len(subjects))
	for _, s := range subjects {
		identity := Identity{NHLPlayerID: s.NHLPlayerID, YahooPlayerID: s.YahooPlayerID, Name: s.Name}
		if idx, ok := d.lookup(s.NHLPlayerID, s.YahooPlayerID); ok {
			identity = d.identities[idx]
		}
		method := MethodSourceNHLID
		switch {
		case s.InBody:
			method = MethodBodyNHLID
		case s.NHLPlayerID == 0:
			method = MethodSourceYahooID
		}
		role := RoleSubject
		if (s.InBody || tagged > 1) && !surnameIn(identity.Name, titleWords) && !surnameIn(s.Name, titleWords) {
			role = RoleMentioned
		}
		text := s.Name
		if text == "" {
			text = identity.Name
		}
		mentions = append(mentions, Mention{
			Text: text, Role: role, Resolution: ResolutionResolved, Method: method, Player: identity,
		})
	}
	return mentions
}

// resolveName decides which player a matched name is.
func (d *Directory) resolveName(hit nameHit, role Role, hints, teams []string) Mention {
	candidates := make([]Identity, 0, len(hit.idxs))
	for _, idx := range hit.idxs {
		candidates = append(candidates, d.identities[idx])
	}
	mention := Mention{Text: hit.name, Role: role, Resolution: ResolutionAmbiguous, Candidates: candidates}
	if len(candidates) == 1 {
		c := candidates[0]
		if len(hints) > 0 && c.Team != "" && !slices.Contains(hints, c.Team) {
			mention.Method = MethodTeamConflict
			return mention
		}
		mention.Resolution, mention.Method, mention.Player, mention.Candidates = ResolutionResolved, MethodName, c, nil
		return mention
	}
	var onTeam []Identity
	for _, c := range candidates {
		if c.Team != "" && slices.Contains(teams, c.Team) {
			onTeam = append(onTeam, c)
		}
	}
	if len(onTeam) == 1 {
		mention.Resolution, mention.Method, mention.Player, mention.Candidates = ResolutionResolved, MethodNameTeam, onTeam[0], nil
		return mention
	}
	mention.Method = MethodName
	return mention
}

// storyTeams returns the teams the source attached plus those the story
// names.
func (d *Directory) storyTeams(story Story, titleWords []string) []string {
	teams := slices.Clone(story.TeamHints)
	for _, words := range [][]string{titleWords, Words(story.Text)} {
		for _, abbrev := range d.findTeams(words) {
			if !slices.Contains(teams, abbrev) {
				teams = append(teams, abbrev)
			}
		}
	}
	return teams
}

// coversKnown reports whether a name hit is a player the source already
// tagged.
func (d *Directory) coversKnown(hit nameHit, known map[string]bool) bool {
	for _, idx := range hit.idxs {
		if known[d.identities[idx].Key()] {
			return true
		}
	}
	return false
}

func (d *Directory) prefixKnown(prefix string, known map[string]bool) bool {
	for _, idx := range d.byName[prefix] {
		if known[d.identities[idx].Key()] {
			return true
		}
	}
	return false
}

type nameHit struct {
	name string
	idxs []int
}

// findNames returns the known full names in words, longest match first and
// without overlaps.
func (d *Directory) findNames(words []string) []nameHit {
	var hits []nameHit
	for i := 0; i < len(words); {
		matched := false
		for n := min(maxNameWords, len(words)-i); n >= minNameWords; n-- {
			key := strings.Join(words[i:i+n], " ")
			if idxs, ok := d.byName[key]; ok {
				hits = append(hits, nameHit{name: key, idxs: idxs})
				i += n
				matched = true
				break
			}
		}
		if !matched {
			i++
		}
	}
	return hits
}

// maxTeamNameWords bounds a team name ("columbus blue jackets").
const maxTeamNameWords = 3

func (d *Directory) findTeams(words []string) []string {
	var teams []string
	for i := range words {
		for n := min(maxTeamNameWords, len(words)-i); n >= 1; n-- {
			if abbrev, ok := d.teams[strings.Join(words[i:i+n], " ")]; ok {
				teams = append(teams, abbrev)
				break
			}
		}
	}
	return teams
}

// titlePrefixName returns the normalized name of a "First Last: headline"
// title, when the part before the colon looks like a person's name.
func titlePrefixName(title string) (string, bool) {
	prefix, _, found := strings.Cut(title, titlePrefixSeparator)
	if !found {
		return "", false
	}
	fields := strings.Fields(prefix)
	if len(fields) < minNameWords || len(fields) > maxNameWords {
		return "", false
	}
	for _, f := range fields {
		if r := []rune(f)[0]; !unicode.IsUpper(r) {
			return "", false
		}
	}
	return strings.Join(Words(prefix), " "), true
}

// surnameIn reports whether the last word of name is one of words.
func surnameIn(name string, words []string) bool {
	nameWords := Words(name)
	return len(nameWords) > 0 && slices.Contains(words, nameWords[len(nameWords)-1])
}
