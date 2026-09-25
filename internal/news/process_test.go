package news

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Players of the acceptance scenarios (NHL IDs; Yahoo IDs where mapped).
const (
	mcnabbID        = 8475188
	lyonID          = 8477494
	lyonYahooID     = 5987
	petterssonCID   = 8480012
	petterssonDID   = 8483678
	ahoCarID        = 8478427
	ahoNyiID        = 8480222
	ferraroID       = 8479983
	wiesblattID     = 8482767
	mckennaYahooID  = 9101
	testWindow      = 14 * 24 * time.Hour
	testProcessedAt = "2026-09-25T12:00:00Z"
)

var retrievedAt = time.Date(2026, time.September, 25, 6, 0, 0, 0, time.UTC)

func testDirectory() *Directory {
	return NewDirectory(
		[]NHLPlayer{
			{ID: mcnabbID, FirstName: "Brayden", LastName: "McNabb", Team: "VGK", Active: true},
			{ID: lyonID, YahooID: lyonYahooID, FirstName: "Alex", LastName: "Lyon", Team: "BUF", Active: true},
			{ID: petterssonCID, FirstName: "Elias", LastName: "Pettersson", Team: "VAN", Active: true},
			{ID: petterssonDID, FirstName: "Elias", LastName: "Pettersson", Team: "VAN", Active: true},
			{ID: ahoCarID, FirstName: "Sebastian", LastName: "Aho", Team: "CAR", Active: true},
			{ID: ahoNyiID, FirstName: "Sebastian", LastName: "Aho", Team: "NYI", Active: true},
			{ID: ferraroID, FirstName: "Mario", LastName: "Ferraro", Team: "SJS", Active: true},
			{ID: wiesblattID, FirstName: "Ozzy", LastName: "Wiesblatt", Team: "NSH", Active: true},
		},
		[]YahooPlayer{
			{YahooPlayerID: lyonYahooID, NHLPlayerID: lyonID, Name: "Alex Lyon", Team: "BUF"},
			{YahooPlayerID: mckennaYahooID, Name: "Gavin McKenna", Team: "TOR"},
		},
		[]Team{
			{Abbrev: "VGK", FullName: "Vegas Golden Knights", CommonName: "Golden Knights"},
			{Abbrev: "CAR", FullName: "Carolina Hurricanes", CommonName: "Hurricanes"},
			{Abbrev: "NYI", FullName: "New York Islanders", CommonName: "Islanders"},
			{Abbrev: "VAN", FullName: "Vancouver Canucks", CommonName: "Canucks"},
			{Abbrev: "SJS", FullName: "San Jose Sharks", CommonName: "Sharks"},
			{Abbrev: "BUF", FullName: "Buffalo Sabres", CommonName: "Sabres"},
			{Abbrev: "NSH", FullName: "Nashville Predators", CommonName: "Predators"},
		},
	)
}

func testSource(t *testing.T, id string) Source {
	t.Helper()
	sources, err := DefaultSources()
	require.NoError(t, err)
	for _, s := range sources {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no default source %s", id)
	return Source{}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return data
}

func nhlFixtureItems(t *testing.T) []Item {
	t.Helper()
	items, err := ParseNHLContent(readFixture(t, "nhl-player-safety.json"), retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	return items
}

func rotowireFixtureItems(t *testing.T) []Item {
	t.Helper()
	items, err := ParseFeed(readFixture(t, "rotowire.xml"), retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	return items
}

func ingest(t *testing.T, store *fakeStore, src Source, items []Item) IngestResult {
	t.Helper()
	result, err := StoreItems(context.Background(), store, src, items)
	require.NoError(t, err)
	return result
}

func processAll(t *testing.T, store *fakeStore) ProcessOutcome {
	t.Helper()
	processedAt, err := time.Parse(time.RFC3339, testProcessedAt)
	require.NoError(t, err)
	var total ProcessOutcome
	dir := testDirectory()
	for _, row := range store.unprocessed() {
		outcome, err := ProcessVersion(context.Background(), store, dir, row, testWindow, processedAt)
		require.NoError(t, err)
		total.Add(outcome)
	}
	return total
}

func playerNews(t *testing.T, store *fakeStore, nhlID int64) PlayerNews {
	t.Helper()
	news, err := LoadPlayerNews(context.Background(), store, Identity{NHLPlayerID: nhlID}, nil)
	require.NoError(t, err)
	return news
}

func TestSyndicatedAndRepeatedReportsMakeOneIncident(t *testing.T) {
	store := newFakeStore()
	ingest(t, store, testSource(t, "nhl-player-safety"), nhlFixtureItems(t))
	ingest(t, store, testSource(t, "rotowire-nhl"), rotowireFixtureItems(t))
	processAll(t, store)

	news := playerNews(t, store, mcnabbID)
	require.Len(t, news.Incidents, 1, "the league story, its team-site copy and RotoWire's report are one suspension")
	inc := news.Incidents[0]
	assert.Equal(t, CategorySuspension, inc.Category)
	assert.Equal(t, []string{"NHL.com", "RotoWire"}, inc.IndependentPublishers())
	relations := map[Relation]int{}
	for _, e := range inc.Evidence {
		relations[e.Relation]++
		assert.NotEmpty(t, e.URL, "every report keeps its link")
		assert.Equal(t, retrievedAt, e.RetrievedAt)
	}
	assert.Equal(t, map[Relation]int{RelationIndependent: 2, RelationSyndicated: 1}, relations)
	assert.Equal(t, time.Date(2026, time.September, 22, 17, 0, 0, 0, time.UTC), inc.FirstReportedAt,
		"the incident starts at the official publication time")
	assert.Equal(t, time.Date(2026, time.September, 22, 18, 15, 0, 0, time.UTC), inc.LastReportedAt,
		"RotoWire's 11:15 AM PDT report is the latest")

	assert.Empty(t, store.incidentsOf(8484823), "a fine is not a suspension, injury or other incident")
}

func TestUnchangedArticlesAreNotReprocessedAndUpdatesAre(t *testing.T) {
	store := newFakeStore()
	src := testSource(t, "nhl-player-safety")
	first := ingest(t, store, src, nhlFixtureItems(t))
	assert.Equal(t, IngestResult{Items: 3, NewVersions: 3}, first)
	processAll(t, store)

	restamped := nhlFixtureItems(t)
	restamped[0].UpdatedAt = restamped[0].UpdatedAt.Add(time.Hour)
	restamped[0].RetrievedAt = retrievedAt.Add(time.Hour)
	again := ingest(t, store, src, restamped)
	assert.Equal(t, IngestResult{Items: 3, Unchanged: 3}, again, "a re-stamped but identical story is not a new version")
	assert.Empty(t, store.unprocessed())

	corrected := nhlFixtureItems(t)
	corrected[0].Text = strings.Replace(corrected[0].Text, "three preseason", "two preseason", 1)
	updated := ingest(t, store, src, corrected)
	assert.Equal(t, 1, updated.NewVersions)
	require.Len(t, store.unprocessed(), 1, "only the corrected story is processed again")
	outcome := processAll(t, store)
	assert.Equal(t, 0, outcome.IncidentsCreated)
	assert.Equal(t, 1, outcome.Repeats)

	news := playerNews(t, store, mcnabbID)
	require.Len(t, news.Incidents, 1)
	var revision, outdated int
	for _, e := range news.Incidents[0].Evidence {
		if e.Relation == RelationRevision {
			revision++
		}
		if e.Outdated() {
			outdated++
		}
	}
	assert.Equal(t, 1, revision, "the correction joins its incident as a revision, not a new source")
	assert.Equal(t, 1, outdated, "the first version is kept and marked outdated")
	assert.Equal(t, []string{"NHL.com"}, news.Incidents[0].IndependentPublishers())
}

func TestCorrectionThatNoLongerReportsTheEventSupersedesIt(t *testing.T) {
	store := newFakeStore()
	src := testSource(t, "rotowire-nhl")
	item := Item{
		ExternalID: "nhl700001", URL: "https://example.test/lyon", Title: "Alex Lyon: Hurt in practice",
		Text: "Lyon was hurt in practice Tuesday.", PublishedAt: retrievedAt.Add(-time.Hour), RetrievedAt: retrievedAt,
	}
	ingest(t, store, src, []Item{normalizeItem(item)})
	processAll(t, store)
	item.Title, item.Text = "Alex Lyon: Healthy scratch Tuesday", "Lyon was a healthy scratch Tuesday, the team clarified."
	ingest(t, store, src, []Item{normalizeItem(item)})
	processAll(t, store)

	news := playerNews(t, store, lyonID)
	require.Len(t, news.Incidents, 1)
	assert.True(t, news.Incidents[0].Superseded(), "the only report behind the injury was corrected")
}

func TestOfficialEventsKeepAttributionAndChronology(t *testing.T) {
	store := newFakeStore()
	official := testSource(t, "nhl-transactions")
	mon := time.Date(2026, time.September, 21, 16, 0, 0, 0, time.UTC)
	tue := mon.Add(24 * time.Hour)
	// The reinstatement is retrieved and processed before the older
	// suspension story; chronology follows publication, not retrieval.
	ingest(t, store, official, []Item{normalizeItem(Item{
		ExternalID: "reinstated", URL: "https://www.nhl.com/news/mcnabb-reinstated", Title: "Brayden McNabb reinstated by NHL",
		Text: "The NHL reinstated Vegas defenseman Brayden McNabb after his appeal.", PublishedAt: tue, RetrievedAt: tue,
		Subjects: []Subject{{NHLPlayerID: mcnabbID, Name: "Brayden McNabb"}},
	})})
	processAll(t, store)
	ingest(t, store, official, []Item{normalizeItem(Item{
		ExternalID: "suspended", URL: "https://www.nhl.com/news/mcnabb-suspended", Title: "Brayden McNabb suspended three games",
		Text: "Vegas defenseman Brayden McNabb was suspended three games.", PublishedAt: mon, RetrievedAt: tue.Add(24 * time.Hour),
		Subjects: []Subject{{NHLPlayerID: mcnabbID, Name: "Brayden McNabb"}},
	})})
	ingest(t, store, official, []Item{
		normalizeItem(Item{
			ExternalID: "trade", URL: "https://www.nhl.com/news/canucks-acquire-ferraro", Title: "Canucks acquire Ferraro from Sharks",
			Text: "The Vancouver Canucks acquired defenseman Mario Ferraro from the San Jose Sharks.", PublishedAt: mon, RetrievedAt: tue,
			Subjects: []Subject{{NHLPlayerID: ferraroID, Name: "Mario Ferraro"}}, TeamHints: []string{"VAN", "SJS"},
		}),
		normalizeItem(Item{
			ExternalID: "assign", URL: "https://www.nhl.com/news/predators-assign-wiesblatt", Title: "Predators assign Ozzy Wiesblatt to Milwaukee",
			Text: "The Nashville Predators assigned forward Ozzy Wiesblatt to Milwaukee of the AHL.", PublishedAt: tue, RetrievedAt: tue,
			Subjects: []Subject{{NHLPlayerID: wiesblattID, Name: "Ozzy Wiesblatt"}},
		}),
	})
	processAll(t, store)

	mcnabb := playerNews(t, store, mcnabbID)
	require.Len(t, mcnabb.Incidents, 2)
	assert.Equal(t, CategorySuspension, mcnabb.Incidents[0].Category, "ordered by publication: suspension first")
	assert.Equal(t, mon, mcnabb.Incidents[0].FirstReportedAt)
	assert.Equal(t, tue.Add(24*time.Hour), mcnabb.Incidents[0].Evidence[0].RetrievedAt, "retrieval time is kept apart")
	assert.Equal(t, CategoryReinstatement, mcnabb.Incidents[1].Category)
	assert.Equal(t, KindOfficial, mcnabb.Incidents[1].Evidence[0].Kind)

	ferraro := playerNews(t, store, ferraroID)
	require.Len(t, ferraro.Incidents, 1)
	assert.Equal(t, CategoryTrade, ferraro.Incidents[0].Category)
	assert.Equal(t, "https://www.nhl.com/news/canucks-acquire-ferraro", ferraro.Incidents[0].Evidence[0].URL)
	wiesblatt := playerNews(t, store, wiesblattID)
	require.Len(t, wiesblatt.Incidents, 1)
	assert.Equal(t, CategoryRoleChange, wiesblatt.Incidents[0].Category)
}

func TestInjuryAfterReinstatementIsANewIncident(t *testing.T) {
	store := newFakeStore()
	src := testSource(t, "rotowire-nhl")
	day := func(n int) time.Time { return retrievedAt.Add(time.Duration(n) * 24 * time.Hour) }
	for i, entry := range []struct{ title, text string }{
		{"Alex Lyon: Hurt in Thursday's game", "Lyon sustained an upper-body injury."},
		{"Alex Lyon: Cleared to play", "Lyon was cleared to play Saturday."},
		{"Alex Lyon: Injured again", "Lyon left Monday's game with a lower-body injury."},
	} {
		ingest(t, store, src, []Item{normalizeItem(Item{
			ExternalID: entry.title, Title: entry.title, Text: entry.text, PublishedAt: day(2 * i), RetrievedAt: day(2 * i),
		})})
		processAll(t, store)
	}
	var categories []Category
	for _, inc := range playerNews(t, store, lyonID).Incidents {
		categories = append(categories, inc.Category)
	}
	assert.Equal(t, []Category{CategoryInjury, CategoryReinstatement, CategoryInjury}, categories)
}

func TestAmbiguousAndUnknownPlayersAreNotAttached(t *testing.T) {
	store := newFakeStore()
	ingest(t, store, testSource(t, "rotowire-nhl"), rotowireFixtureItems(t))
	outcome := processAll(t, store)

	assert.Empty(t, store.incidentsOf(petterssonCID), "two Elias Petterssons play for Vancouver")
	assert.Empty(t, store.incidentsOf(petterssonDID))
	assert.Len(t, store.incidentsOf(ahoCarID), 1, "the story names the Hurricanes, so it is Carolina's Aho")
	assert.Empty(t, store.incidentsOf(ahoNyiID))
	assert.Positive(t, outcome.Ambiguous)
	assert.Positive(t, outcome.Unresolved)

	issues, err := LoadMentionIssues(context.Background(), store, retrievedAt.Add(-time.Hour), defaultMaxItems)
	require.NoError(t, err)
	byName := map[string]MentionIssue{}
	for _, is := range issues {
		byName[is.Mention] = is
	}
	require.Contains(t, byName, "elias pettersson")
	assert.Equal(t, ResolutionAmbiguous, byName["elias pettersson"].Resolution)
	assert.Len(t, byName["elias pettersson"].Candidates, 2)
	require.Contains(t, byName, "ivan ryabkin", "an unfamiliar rookie stays visible, unattached")
	assert.Equal(t, ResolutionUnresolved, byName["ivan ryabkin"].Resolution)
	assert.Equal(t, MethodTitlePrefix, byName["ivan ryabkin"].Method)
}

func TestYahooStatusIsOneIndependentSourceAndClearingIsRecorded(t *testing.T) {
	store := newFakeStore()
	yahoo := testSource(t, "yahoo-status")
	ingest(t, store, testSource(t, "rotowire-nhl"), rotowireFixtureItems(t))
	dtd := YahooStatus{YahooPlayerID: lyonYahooID, Name: "Alex Lyon", Status: "DTD", StatusFull: "Day-to-Day",
		InjuryNote: "Upper Body", FetchedAt: retrievedAt}
	healthy := YahooStatus{YahooPlayerID: mckennaYahooID, Name: "Gavin McKenna", FetchedAt: retrievedAt}
	stored := ingest(t, store, yahoo, YahooStatusItems([]YahooStatus{dtd, healthy}))
	assert.Equal(t, IngestResult{Items: 2, NewVersions: 1, Skipped: 1}, stored,
		"a player without a status is not a story unless a status was listed before")
	processAll(t, store)

	news := playerNews(t, store, lyonID)
	require.Len(t, news.Incidents, 1)
	assert.Equal(t, []string{"RotoWire", "Yahoo Fantasy"}, news.Incidents[0].IndependentPublishers())

	cleared := dtd
	cleared.Status, cleared.StatusFull, cleared.InjuryNote = "", "", ""
	cleared.FetchedAt = retrievedAt.Add(48 * time.Hour)
	stored = ingest(t, store, yahoo, YahooStatusItems([]YahooStatus{cleared}))
	assert.Equal(t, 1, stored.NewVersions)
	processAll(t, store)
	news = playerNews(t, store, lyonID)
	require.Len(t, news.Incidents, 2)
	assert.Equal(t, CategoryReinstatement, news.Incidents[1].Category)
	assert.Equal(t, KindStructured, news.Incidents[1].Evidence[0].Kind)
}

func TestReprocessingAVersionAddsNoDuplicateEvidence(t *testing.T) {
	store := newFakeStore()
	ingest(t, store, testSource(t, "nhl-player-safety"), nhlFixtureItems(t))
	rows := store.unprocessed()
	processAll(t, store)
	dir := testDirectory()
	for _, row := range rows {
		_, err := ProcessVersion(context.Background(), store, dir, row, testWindow, retrievedAt)
		require.NoError(t, err)
	}
	news := playerNews(t, store, mcnabbID)
	require.Len(t, news.Incidents, 1)
	assert.Len(t, news.Incidents[0].Evidence, 2, "a retried activity re-running a version changes nothing")
}

func TestClearedYahooStatusDoesNotSupersedeTheInjury(t *testing.T) {
	store := newFakeStore()
	yahoo := testSource(t, "yahoo-status")
	out := YahooStatus{YahooPlayerID: mckennaYahooID, Name: "Gavin McKenna", Status: "O", StatusFull: "Out", FetchedAt: retrievedAt}
	ingest(t, store, yahoo, YahooStatusItems([]YahooStatus{out}))
	processAll(t, store)
	cleared := YahooStatus{YahooPlayerID: mckennaYahooID, Name: "Gavin McKenna", FetchedAt: retrievedAt.Add(72 * time.Hour)}
	ingest(t, store, yahoo, YahooStatusItems([]YahooStatus{cleared}))
	processAll(t, store)

	news, err := LoadPlayerNews(context.Background(), store, Identity{YahooPlayerID: mckennaYahooID}, nil)
	require.NoError(t, err)
	require.Len(t, news.Incidents, 2, "a Yahoo-only rookie's injury and its end")
	injury := news.Incidents[0]
	assert.Equal(t, CategoryInjury, injury.Category)
	assert.True(t, injury.Evidence[0].Outdated())
	assert.False(t, injury.Superseded(), "the status ending is a change of state, not a correction of the injury")
	assert.Equal(t, CategoryReinstatement, news.Incidents[1].Category)
}
