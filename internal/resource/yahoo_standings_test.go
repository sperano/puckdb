package resource_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var standingsCaptureDay = time.Date(2026, time.November, 15, 0, 0, 0, 0, time.UTC)

func h2hStandings() resource.LeagueStandings {
	return resource.LeagueStandings{
		Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID,
		Date: standingsCaptureDay, GameKey: yahoofixtures.PointsGameKey,
	}
}

func TestLeagueStandings_Path(t *testing.T) {
	t.Parallel()
	r := h2hStandings()
	assert.Equal(t, "seasons/2026/yahoo/77777/standings/standings-2026-11-15.xml", r.Path())
	// The path names the explicit season, not one deduced from the date.
	r.Season = 2025
	assert.Equal(t, "seasons/2025/yahoo/77777/standings/standings-2026-11-15.xml", r.Path())
}

func TestLeagueStandings_URL(t *testing.T) {
	t.Parallel()
	r := h2hStandings()
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/league/465.l.77777/standings", r.URL())
	assert.NotContains(t, r.URL(), "date", "Yahoo standings take no date filter")
}

func TestLeagueStandings_Type(t *testing.T) {
	t.Parallel()
	assert.Equal(t, core.YahooLeagueStandings, h2hStandings().Type())
}

func TestStandingsCaptureDate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"afternoon pacific", time.Date(2026, time.October, 22, 22, 0, 0, 0, time.UTC),
			time.Date(2026, time.October, 22, 0, 0, 0, 0, time.UTC)},
		{"late evening pacific is still the same day", time.Date(2026, time.October, 23, 6, 59, 0, 0, time.UTC),
			time.Date(2026, time.October, 22, 0, 0, 0, 0, time.UTC)},
		{"midnight pacific starts the next day", time.Date(2026, time.October, 23, 7, 0, 0, 0, time.UTC),
			time.Date(2026, time.October, 23, 0, 0, 0, 0, time.UTC)},
		{"standard time offset", time.Date(2026, time.December, 1, 7, 30, 0, 0, time.UTC),
			time.Date(2026, time.November, 30, 0, 0, 0, 0, time.UTC)},
		{"non-utc input", time.Date(2026, time.October, 23, 2, 30, 0, 0, time.FixedZone("EDT", -4*3600)),
			time.Date(2026, time.October, 22, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resource.StandingsCaptureDate(tt.now)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, time.UTC, got.Location())
		})
	}
}

func TestLeagueStandings_Parse_H2H(t *testing.T) {
	t.Parallel()
	content, err := h2hStandings().Parse(yahoofixtures.Read(yahoofixtures.H2HStandings))
	require.NoError(t, err)
	require.NotNil(t, content)
	assert.Equal(t, "465.l.77777", content.League.Key)
	records, err := resource.TeamStandingRecords(content.League)
	require.NoError(t, err)
	require.Len(t, records, yahoofixtures.TeamsInH2HStandings)

	first := records[0]
	assert.Equal(t, 3, first.TeamID)
	assert.Equal(t, "465.l.77777.t.3", first.TeamKey)
	assert.Equal(t, "Ice Breakers", first.Name)
	assert.Equal(t, 1, first.Rank)
	assert.Equal(t, 1, first.PlayoffSeed)
	require.NotNil(t, first.Outcome)
	assert.Equal(t, 4, first.Outcome.Wins)
	assert.Equal(t, 1, first.Outcome.Losses)
	assert.Equal(t, 0, first.Outcome.Ties)
	require.NotNil(t, first.Outcome.Percentage)
	assert.InDelta(t, 0.8, *first.Outcome.Percentage, 1e-9)
	assert.Nil(t, first.DivisionalOutcome, "no divisional record in the fixture for this team")

	second := records[1]
	assert.Equal(t, 1, second.TeamID)
	assert.Equal(t, 2, second.Rank)
	require.NotNil(t, second.Outcome)
	assert.Equal(t, 1, second.Outcome.Ties)
	require.NotNil(t, second.DivisionalOutcome)
	assert.Equal(t, 2, second.DivisionalOutcome.Wins)
	assert.Nil(t, second.DivisionalOutcome.Percentage, "Yahoo serves no divisional percentage")

	ranks := make([]int, 0, len(records))
	for _, record := range records {
		ranks = append(ranks, record.Rank)
	}
	assert.Equal(t, []int{1, 2, 3, 4}, ranks, "records keep Yahoo's rank order")
}

// Before the first game Yahoo serves empty ranks, seeds and percentages:
// they read as 0 and nil, not as errors.
func TestLeagueStandings_Parse_Preseason(t *testing.T) {
	t.Parallel()
	content, err := h2hStandings().Parse(yahoofixtures.Read(yahoofixtures.PreseasonStandings))
	require.NoError(t, err)
	records, err := resource.TeamStandingRecords(content.League)
	require.NoError(t, err)
	require.Len(t, records, 2)
	for _, record := range records {
		assert.Equal(t, 0, record.Rank)
		assert.Equal(t, 0, record.PlayoffSeed)
		require.NotNil(t, record.Outcome)
		assert.Equal(t, resource.OutcomeRecord{}, *record.Outcome)
	}
}

// A rotisserie league ranks its teams without a head-to-head record.
func TestLeagueStandings_Parse_Roto(t *testing.T) {
	t.Parallel()
	r := resource.LeagueStandings{
		Season: yahoofixtures.RotoSeason, LeagueID: yahoofixtures.RotoLeagueID,
		Date: time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC), GameKey: yahoofixtures.RotoGameKey,
	}
	content, err := r.Parse(yahoofixtures.Read(yahoofixtures.RotoStandings))
	require.NoError(t, err)
	records, err := resource.TeamStandingRecords(content.League)
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, 5, records[0].TeamID)
	assert.Equal(t, 1, records[0].Rank)
	assert.Nil(t, records[0].Outcome)
	assert.Nil(t, records[0].DivisionalOutcome)
	assert.Equal(t, 2, records[1].Rank)
}

func TestLeagueStandings_Parse_RejectsOtherLeague(t *testing.T) {
	t.Parallel()
	data := yahoofixtures.Read(yahoofixtures.H2HStandings)
	tests := []struct {
		name    string
		mutate  func(r *resource.LeagueStandings)
		wantErr string
	}{
		{"league id", func(r *resource.LeagueStandings) { r.LeagueID++ }, "does not match requested league"},
		{"season", func(r *resource.LeagueStandings) { r.Season-- }, "does not match requested season"},
		{"game key", func(r *resource.LeagueStandings) { r.GameKey++ }, "verified game key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := h2hStandings()
			tt.mutate(&r)
			_, err := r.Parse(data)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Contains(t, err.Error(), "2026-11-15", "the error names the capture day")
		})
	}
}

// standingsDoc wraps teams XML in the fixture league's standings envelope.
func standingsDoc(teamsAttr, teams string) []byte {
	return []byte(`<fantasy_content><league>
<league_key>465.l.77777</league_key><league_id>77777</league_id><game_code>nhl</game_code><season>2026</season>
<standings><teams` + teamsAttr + `>` + teams + `</teams></standings></league></fantasy_content>`)
}

const standingsTeam = `<team><team_key>465.l.77777.t.1</team_key><team_id>1</team_id><name>Slap Shots</name>
<team_standings><rank>1</rank><playoff_seed>1</playoff_seed>
<outcome_totals><wins>3</wins><losses>1</losses><ties>1</ties><percentage>.700</percentage></outcome_totals>
</team_standings></team>`

func TestLeagueStandings_Parse_RejectsMalformed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"not xml", []byte(invalidXML), "parse standings 2026/77777 for 2026-11-15"},
		{"no standings element", []byte(`<fantasy_content><league><league_key>465.l.77777</league_key>` +
			`<league_id>77777</league_id><game_code>nhl</game_code><season>2026</season></league></fantasy_content>`),
			"contains no standings"},
		{"empty teams", standingsDoc(` count="0"`, ""), "contains no standings"},
		{"count mismatch", standingsDoc(` count="2"`, standingsTeam), "list 1 teams but count says 2"},
		{"team without id", standingsDoc("", strings.Replace(standingsTeam, "<team_id>1</team_id>", "", 1)),
			`team "465.l.77777.t.1" has no team_id`},
		{"team without standings", standingsDoc("", `<team><team_key>465.l.77777.t.1</team_key><team_id>1</team_id></team>`),
			"team 1 has no team_standings"},
		{"duplicate team", standingsDoc("", standingsTeam+standingsTeam), "team 1 listed twice"},
		{"rank not a number", standingsDoc("", strings.Replace(standingsTeam, "<rank>1</rank>", "<rank>first</rank>", 1)),
			`rank "first" is not a non-negative integer`},
		{"negative seed", standingsDoc("", strings.Replace(standingsTeam, "<playoff_seed>1</playoff_seed>",
			"<playoff_seed>-1</playoff_seed>", 1)), `playoff_seed "-1" is not a non-negative integer`},
		{"wins not a number", standingsDoc("", strings.Replace(standingsTeam, "<wins>3</wins>", "<wins>three</wins>", 1)),
			`outcome_totals wins "three" is not a non-negative integer`},
		{"losses not a number", standingsDoc("", strings.Replace(standingsTeam, "<losses>1</losses>", "<losses>x</losses>", 1)),
			`outcome_totals losses "x"`},
		{"ties not a number", standingsDoc("", strings.Replace(standingsTeam, "<ties>1</ties>", "<ties>1.5</ties>", 1)),
			`outcome_totals ties "1.5"`},
		{"percentage not a number", standingsDoc("", strings.Replace(standingsTeam, "<percentage>.700</percentage>",
			"<percentage>70%</percentage>", 1)), `outcome_totals percentage "70%" is not a number`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			content, err := h2hStandings().Parse(tt.data)
			require.Error(t, err)
			assert.Nil(t, content)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Optional fields Yahoo leaves out or empty read as absent, not as errors.
func TestLeagueStandings_Parse_OptionalFields(t *testing.T) {
	t.Parallel()
	team := `<team><team_key>465.l.77777.t.1</team_key><team_id>1</team_id><name>Slap Shots</name>
<team_standings><rank>1</rank><outcome_totals><wins>2</wins><losses>1</losses></outcome_totals></team_standings></team>`
	content, err := h2hStandings().Parse(standingsDoc("", team))
	require.NoError(t, err)
	records, err := resource.TeamStandingRecords(content.League)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, 0, records[0].PlayoffSeed, "no playoff_seed element")
	require.NotNil(t, records[0].Outcome)
	assert.Equal(t, 0, records[0].Outcome.Ties, "no ties element")
	assert.Nil(t, records[0].Outcome.Percentage, "no percentage element")
	assert.Nil(t, records[0].DivisionalOutcome)
}

// TeamStandingRecords is the typed view the importer reads: a document whose
// standings were never filled (a settings file, say) is rejected too.
func TestTeamStandingRecords_NoStandings(t *testing.T) {
	t.Parallel()
	_, err := resource.TeamStandingRecords(store.League{ID: 77777})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains no standings")
}
