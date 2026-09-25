package news

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	retiredBrownID = 8465000
	activeBrownID  = 8481000
)

func mentionByText(t *testing.T, mentions []Mention, text string) Mention {
	t.Helper()
	for _, m := range mentions {
		if m.Text == text {
			return m
		}
	}
	t.Fatalf("no mention %q in %+v", text, mentions)
	return Mention{}
}

func TestResolveIgnoresRetiredNamesakes(t *testing.T) {
	dir := NewDirectory([]NHLPlayer{
		{ID: retiredBrownID, FirstName: "Mike", LastName: "Brown", Team: "TOR", Active: false},
	}, nil, nil)
	mentions := dir.Resolve(Story{Title: "Mike Brown: Makes NHL debut", Text: "The rookie Brown played 12 minutes."})
	m := mentionByText(t, mentions, "mike brown")
	assert.Equal(t, ResolutionUnresolved, m.Resolution, "a retired player must not capture a rookie's story")
	assert.Equal(t, MethodTitlePrefix, m.Method)
}

func TestResolveFlagsATeamConflictInsteadOfGuessing(t *testing.T) {
	dir := NewDirectory([]NHLPlayer{
		{ID: activeBrownID, FirstName: "Mike", LastName: "Brown", Team: "TOR", Active: true},
	}, nil, nil)
	mentions := dir.Resolve(Story{
		Title: "Mike Brown scores in debut", Text: "Mike Brown scored for the Kraken.", TeamHints: []string{"SEA"},
	})
	m := mentionByText(t, mentions, "mike brown")
	assert.Equal(t, ResolutionAmbiguous, m.Resolution, "the source says Seattle, the only Mike Brown known plays for Toronto")
	assert.Equal(t, MethodTeamConflict, m.Method)
	require.Len(t, m.Candidates, 1)
}

func TestResolveUsesTheYahooPoolForRookiesWithoutNHLRecords(t *testing.T) {
	dir := testDirectory()
	mentions := dir.Resolve(Story{Title: "Gavin McKenna: Signs entry-level deal", Text: "McKenna signed with Toronto."})
	m := mentionByText(t, mentions, "gavin mckenna")
	assert.Equal(t, ResolutionResolved, m.Resolution)
	assert.Equal(t, Identity{YahooPlayerID: mckennaYahooID, Name: "Gavin McKenna", Team: "TOR"}, m.Player)
	assert.Equal(t, "yahoo:9101", m.Player.Key())
}

func TestResolveMergesYahooAndNHLNames(t *testing.T) {
	dir := NewDirectory(
		[]NHLPlayer{{ID: lyonID, FirstName: "Alexander", LastName: "Lyon", Team: "BUF", Active: true}},
		[]YahooPlayer{{YahooPlayerID: lyonYahooID, NHLPlayerID: lyonID, Name: "Alex Lyon", Team: "Buf"}}, nil)
	for _, title := range []string{"Alex Lyon: Starts Thursday", "Alexander Lyon: Starts Thursday"} {
		m := dir.Resolve(Story{Title: title})[0]
		assert.Equal(t, ResolutionResolved, m.Resolution, title)
		assert.Equal(t, int64(lyonID), m.Player.NHLPlayerID)
		assert.Equal(t, lyonYahooID, m.Player.YahooPlayerID)
	}
}

func TestResolveNeverMatchesASurnameAlone(t *testing.T) {
	mentions := testDirectory().Resolve(Story{Title: "Injury update", Text: "Lyon and McNabb both skated Friday."})
	assert.Empty(t, mentions)
}

func TestResolveRolesOfTaggedPlayers(t *testing.T) {
	story := Story{
		Title: "Maurice provides injury updates on McNabb",
		Subjects: []Subject{
			{NHLPlayerID: mcnabbID, Name: "Brayden McNabb"},
			{NHLPlayerID: ferraroID, Name: "Mario Ferraro"},
		},
	}
	mentions := testDirectory().Resolve(story)
	assert.Equal(t, RoleSubject, mentionByText(t, mentions, "Brayden McNabb").Role, "tagged and named in the title")
	assert.Equal(t, RoleMentioned, mentionByText(t, mentions, "Mario Ferraro").Role, "tagged only")
	assert.Equal(t, MethodSourceNHLID, mentionByText(t, mentions, "Mario Ferraro").Method)
}

func TestResolveTaggedPlayerUnknownLocallyKeepsTheSourceID(t *testing.T) {
	const newRookieID = 8486067
	mentions := testDirectory().Resolve(Story{
		Title: "Rookie signs", Subjects: []Subject{{NHLPlayerID: newRookieID, Name: "New Rookie"}},
	})
	require.Len(t, mentions, 1)
	assert.Equal(t, ResolutionResolved, mentions[0].Resolution, "the source's own NHL ID is authoritative")
	assert.Equal(t, int64(newRookieID), mentions[0].Player.NHLPlayerID)
}

func TestResolveStructuredStoriesUseIDsOnly(t *testing.T) {
	mentions := testDirectory().Resolve(Story{
		Title: "Alex Lyon: Day-to-Day", Text: "Mario Ferraro Brayden McNabb", Structured: true,
		Subjects: []Subject{{YahooPlayerID: lyonYahooID, Name: "Alex Lyon"}},
	})
	require.Len(t, mentions, 1)
	assert.Equal(t, int64(lyonID), mentions[0].Player.NHLPlayerID, "the Yahoo ID maps to the NHL player")
	assert.Equal(t, MethodSourceYahooID, mentions[0].Method)
}

func TestResolvePromotesTheOnlyPlayerNamedInTheText(t *testing.T) {
	mentions := testDirectory().Resolve(Story{
		Title: "Canucks acquire defenceman in trade with Sharks",
		Text:  "The Vancouver Canucks acquired Mario Ferraro from the San Jose Sharks.",
	})
	require.Len(t, mentions, 1)
	assert.Equal(t, RoleSubject, mentions[0].Role)

	two := testDirectory().Resolve(Story{Title: "Trade", Text: "Mario Ferraro and Brayden McNabb were traded."})
	for _, m := range two {
		assert.Equal(t, RoleMentioned, m.Role, "with two players named, neither is presumed the subject")
	}
}

func TestNHLTeamAbbrev(t *testing.T) {
	assert.Equal(t, "NJD", NHLTeamAbbrev("nj"))
	assert.Equal(t, "TOR", NHLTeamAbbrev("Tor"))
}

func TestResolveBodyLinkedPlayerIsASubjectOnlyWhenTheTitleNamesThem(t *testing.T) {
	const (
		taggedID = 8475188
		namedID  = 8482124
		otherID  = 8479999
	)
	dir := NewDirectory(nil, nil, nil)
	mentions := dir.Resolve(Story{
		Title: "McNabb suspended for hit on Byfield",
		Subjects: []Subject{
			{NHLPlayerID: taggedID, Name: "Brayden McNabb"},
			{NHLPlayerID: namedID, Name: "Quinton Byfield", InBody: true},
			{NHLPlayerID: otherID, Name: "Adrian Kempe", InBody: true},
		},
	})
	tagged := mentionByText(t, mentions, "Brayden McNabb")
	assert.Equal(t, RoleSubject, tagged.Role, "body links do not demote the only tagged player")
	assert.Equal(t, MethodSourceNHLID, tagged.Method)

	named := mentionByText(t, mentions, "Quinton Byfield")
	assert.Equal(t, RoleSubject, named.Role)
	assert.Equal(t, MethodBodyNHLID, named.Method)
	assert.Equal(t, ResolutionResolved, named.Resolution)

	other := mentionByText(t, mentions, "Adrian Kempe")
	assert.Equal(t, RoleMentioned, other.Role)
	assert.Equal(t, MethodBodyNHLID, other.Method)
}

func TestResolvePromotesTheOnlyBodyLinkedPlayerOfAnUntaggedStory(t *testing.T) {
	const signedID = 8480001
	dir := NewDirectory(nil, nil, nil)
	mentions := dir.Resolve(Story{
		Title:    "Canucks sign defenseman to two-year contract",
		Subjects: []Subject{{NHLPlayerID: signedID, Name: "Mario Ferraro", InBody: true}},
	})
	require.Len(t, mentions, 1)
	assert.Equal(t, RoleSubject, mentions[0].Role, "the only player a story names is its subject")
}
