package news

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNHLContentFixture(t *testing.T) {
	items, err := ParseNHLContent(readFixture(t, "nhl-player-safety.json"), retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 3)

	first := items[0]
	assert.Equal(t, "a1f0c7e2-0001-4b9a-9d40-000000000001", first.ExternalID)
	assert.Equal(t, "https://www.nhl.com/news/brayden-mcnabb-suspended-three-games-for-cross-checking", first.URL)
	assert.Equal(t, "Brayden McNabb suspended three games for cross-checking", first.Title)
	assert.Contains(t, first.Text, "Brayden McNabb has been suspended")
	assert.Equal(t, time.Date(2026, time.September, 22, 17, 0, 0, 0, time.UTC), first.PublishedAt)
	assert.Equal(t, time.Date(2026, time.September, 22, 17, 5, 0, 0, time.UTC), first.UpdatedAt)
	require.Len(t, first.Subjects, 1)
	assert.Equal(t, Subject{NHLPlayerID: 8475188, Name: "Brayden McNabb"}, first.Subjects[0])
	assert.Equal(t, []string{"VGK"}, first.TeamHints)
}

func TestParseNHLContentTeamHintFromContext(t *testing.T) {
	items, err := ParseNHLContent(readFixture(t, "nhl-player-safety.json"), retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 3)

	second := items[1]
	assert.Equal(t, []string{"VGK"}, second.TeamHints, "the team-site copy has no team tag, only a team context")
	require.Len(t, second.Subjects, 1)
	assert.Equal(t, int64(8475188), second.Subjects[0].NHLPlayerID)
}

func TestParseNHLContentAcceptsNumericPlayerIDInExtraData(t *testing.T) {
	items, err := ParseNHLContent(readFixture(t, "nhl-player-safety.json"), retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 3)

	third := items[2]
	require.Len(t, third.Subjects, 1)
	assert.Equal(t, Subject{NHLPlayerID: 8484823, Name: "Andrew Cristall"}, third.Subjects[0])
}

func TestParseNHLContentInvalidJSONErrors(t *testing.T) {
	_, err := ParseNHLContent([]byte("not json"), retrievedAt, defaultMaxItems)
	require.Error(t, err)
}

func TestParseNHLContentLimitIsHonored(t *testing.T) {
	items, err := ParseNHLContent(readFixture(t, "nhl-player-safety.json"), retrievedAt, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestParseNHLContentReadsTheStoryPageURL(t *testing.T) {
	body := []byte(`{"items":[{"_entityId":"e1","selfUrl":"https://forge.example.com/stories/s1","slug":"s1",
		"headline":"Headline","summary":"Summary."}]}`)
	items, err := ParseNHLContent(body, retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "https://forge.example.com/stories/s1", items[0].BodyURL)
	assert.Empty(t, items[0].Body, "the tag feed carries no body")
}

func TestParseNHLStoryBodyFixture(t *testing.T) {
	text, subjects, err := ParseNHLStoryBody(readFixture(t, "nhl-story-page.json"))
	require.NoError(t, err)
	assert.Contains(t, text, `<forge-entity title="Brayden McNabb"`, "the raw markdown is returned; cleaning is the caller's")
	assert.Contains(t, text, "Players' Emergency Assistance Fund")
	assert.Equal(t, []Subject{
		{NHLPlayerID: mcnabbID, Name: "Brayden McNabb", InBody: true},
		{NHLPlayerID: 8482124, Name: "Quinton Byfield", InBody: true},
	}, subjects, "only player entities are subjects; team entities are not")
}

func TestParseNHLStoryBodySkipsPlayerLinksWithoutAnID(t *testing.T) {
	page := []byte(`{"parts":[{"type":"markdown","content":` +
		`"<forge-entity title=\"A B\" slug=\"a-b\" code=\"player\">A B</forge-entity> and ` +
		`<forge-entity title=\"C &amp; D\" slug=\"c-d-8470001\" code=\"player\">C D</forge-entity>"}]}`)
	_, subjects, err := ParseNHLStoryBody(page)
	require.NoError(t, err)
	assert.Equal(t, []Subject{{NHLPlayerID: 8470001, Name: "C & D", InBody: true}}, subjects)
}

func TestParseNHLStoryBodyInvalidJSONErrors(t *testing.T) {
	_, _, err := ParseNHLStoryBody([]byte("<html>"))
	require.Error(t, err)
}

func TestParseNHLStoryBodyReadsMarkdownPlayerLinks(t *testing.T) {
	page := []byte(`{"parts":[{"type":"customentity","content":{"id":"x"}},{"type":"markdown","content":` +
		`"**NEW YORK --** Seattle Kraken defenseman [Ville Ottavainen](https://www.nhl.com/player/ville-ottavainen-8482866/stats) ` +
		`was fined for an altercation with [Brennan Othmann](https://www.nhl.com/flames/player/brennan-othmann-8482747) of the ` +
		`[Calgary Flames](https://www.nhl.com/flames)."}]}`)
	text, subjects, err := ParseNHLStoryBody(page)
	require.NoError(t, err, "a non-text part whose content is an object is skipped")
	assert.Contains(t, text, "Seattle Kraken defenseman")
	assert.Equal(t, []Subject{
		{NHLPlayerID: 8482866, Name: "Ville Ottavainen", InBody: true},
		{NHLPlayerID: 8482747, Name: "Brennan Othmann", InBody: true},
	}, subjects)
}
