package yahoo

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ────────────────────────────────────────────────────────────────────────────
// XML test fixtures
// ────────────────────────────────────────────────────────────────────────────

const (
	leagueWithPositionsXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <league_id>12345</league_id>
    <league_key>423.l.12345</league_key>
    <name>Test League</name>
    <url>https://hockey.fantasysports.yahoo.com/hockey/12345</url>
    <logo_url>https://example.com/logo.png</logo_url>
    <draft_status>postdraft</draft_status>
    <num_teams>12</num_teams>
    <scoring_type>headpoint</scoring_type>
    <league_type>private</league_type>
    <is_pro_league>0</is_pro_league>
    <is_cash_league>0</is_cash_league>
    <start_date>2023-10-10</start_date>
    <end_date>2024-04-18</end_date>
    <game_code>nhl</game_code>
    <season>2023</season>
    <settings>
      <draft_type>snake</draft_type>
      <is_auction_draft>0</is_auction_draft>
      <roster_positions>
        <roster_position><position>C</position><position_type>P</position_type><count>2</count><is_starting_position>1</is_starting_position></roster_position>
        <roster_position><position>BN</position><position_type>P</position_type><count>4</count><is_starting_position>0</is_starting_position></roster_position>
      </roster_positions>
      <stat_categories>
        <stats>
          <stat><stat_id>1</stat_id><name>Goals</name><abbr>G</abbr><group>Offense</group><enabled>1</enabled></stat>
          <stat><stat_id>2</stat_id><name>Assists</name><abbr>A</abbr><group>Offense</group><enabled>1</enabled></stat>
        </stats>
      </stat_categories>
    </settings>
  </league>
</fantasy_content>`

	teamWithManagerXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <team>
    <team_id>1</team_id>
    <team_key>423.l.12345.t.1</team_key>
    <name>Rocket Surgeons</name>
    <url>https://hockey.fantasysports.yahoo.com/hockey/12345/1</url>
    <team_logos>
      <team_logo><size>small</size><url>https://example.com/logo-small.png</url></team_logo>
      <team_logo><size>large</size><url>https://example.com/logo-large.png</url></team_logo>
    </team_logos>
    <waiver_priority>3</waiver_priority>
    <number_of_moves>15</number_of_moves>
    <number_of_trades>2</number_of_trades>
    <draft_position>4</draft_position>
    <managers>
      <manager>
        <manager_id>1001</manager_id>
        <nickname>CoachFrost</nickname>
        <guid>abc123guid</guid>
        <email>coach@example.com</email>
        <image_url>https://example.com/avatar.png</image_url>
        <felo_score>850</felo_score>
        <felo_tier>Gold</felo_tier>
      </manager>
    </managers>
  </team>
</fantasy_content>`

	summaryWithStatsXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <team>
    <team_stats>
      <coverage_type>date</coverage_type>
      <stats>
        <stat><stat_id>1</stat_id><value>2</value></stat>
        <stat><stat_id>2</stat_id><value>3</value></stat>
      </stats>
    </team_stats>
  </team>
</fantasy_content>`

	rosterWithPlayersXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <team>
    <roster>
      <coverage_type>date</coverage_type>
      <is_editable>1</is_editable>
      <players count="1">
        <player>
          <player_id>6616</player_id>
          <player_key>423.p.6616</player_key>
          <position_type>P</position_type>
          <primary_position>C</primary_position>
          <eligible_positions>C,F,UTIL</eligible_positions>
          <selected_position>
            <coverage_type>date</coverage_type>
            <position>C</position>
            <is_flex>0</is_flex>
          </selected_position>
        </player>
      </players>
    </roster>
  </team>
</fantasy_content>`

	transactionsWithOneXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <transactions count="1">
      <transaction>
        <transaction_key>423.l.12345.tr.1</transaction_key>
        <type>add/drop</type>
        <timestamp>1700000000</timestamp>
        <status>successful</status>
        <players count="0"></players>
      </transaction>
    </transactions>
  </league>
</fantasy_content>`

	draftResultsWithPicksXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <draft_results count="2">
      <draft_result><round>1</round><pick>1</pick><team_key>423.l.12345.t.3</team_key><player_key>423.p.6616</player_key></draft_result>
      <draft_result><round>1</round><pick>2</pick><team_key>423.l.12345.t.1</team_key><player_key>423.p.5441</player_key></draft_result>
    </draft_results>
  </league>
</fantasy_content>`

	matchupsWeek1XML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <scoreboard>
      <week>1</week>
      <matchups count="1">
        <matchup>
          <week>1</week>
          <status>postevent</status>
          <is_playoffs>0</is_playoffs>
          <is_consolation>0</is_consolation>
          <teams count="2">
            <team><team_id>1</team_id><team_points><total>55.5</total></team_points></team>
            <team><team_id>2</team_id><team_points><total>42.0</total></team_points></team>
          </teams>
        </matchup>
      </matchups>
    </scoreboard>
  </league>
</fantasy_content>`

	matchupsWeek3XML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <scoreboard>
      <week>3</week>
      <matchups count="1">
        <matchup>
          <week>3</week>
          <status>postevent</status>
          <is_playoffs>0</is_playoffs>
          <is_consolation>0</is_consolation>
          <teams count="2">
            <team><team_id>3</team_id><team_points><total>61.0</total></team_points></team>
            <team><team_id>4</team_id><team_points><total>48.5</total></team_points></team>
          </teams>
        </matchup>
      </matchups>
    </scoreboard>
  </league>
</fantasy_content>`
)

// ────────────────────────────────────────────────────────────────────────────
// ImportYahooLeague tests
// ────────────────────────────────────────────────────────────────────────────

type ImportYahooLeagueSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportYahooLeagueSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportYahooLeagueSuite(t *testing.T) {
	suite.Run(t, new(ImportYahooLeagueSuite))
}

func (s *ImportYahooLeagueSuite) TestNoFile_ReturnsActionableError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	s.env.RegisterActivity(a.ImportYahooLeague)
	_, err := s.env.ExecuteActivity(a.ImportYahooLeague, ImportYahooLeagueInput{Season: 2023, LeagueID: 12345})

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "required Yahoo league cache is missing")
	q.AssertNotCalled(s.T(), "UpsertYahooLeague")
}

func (s *ImportYahooLeagueSuite) TestWithPositionsAndStats() {
	mem := store.NewMemStorage()
	q := &MockQueries{}

	leagueRes := resource.League{Season: 2023, LeagueID: 12345}
	require.NoError(s.T(), mem.Write(context.Background(), leagueRes.Path(), []byte(leagueWithPositionsXML)))

	q.On("UpsertYahooLeague", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertYahooLeagueParams")).Return(nil)
	q.On("UpsertYahooLeagueRosterPositionBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooLeagueRosterPositionBatchBatchResults(&mockBatchResults{}, 2))
	q.On("UpsertYahooLeagueStatCategoryBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooLeagueStatCategoryBatchBatchResults(&mockBatchResults{}, 2))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	s.env.RegisterActivity(a.ImportYahooLeague)
	val, err := s.env.ExecuteActivity(a.ImportYahooLeague, ImportYahooLeagueInput{Season: 2023, LeagueID: 12345})

	require.NoError(s.T(), err)

	var result ImportYahooLeagueResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), 2, result.RosterPositions)
	assert.Equal(s.T(), 2, result.StatCategories)
	q.AssertExpectations(s.T())
}

func (s *ImportYahooLeagueSuite) TestUpsertLeagueError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}

	leagueRes := resource.League{Season: 2023, LeagueID: 12345}
	require.NoError(s.T(), mem.Write(context.Background(), leagueRes.Path(), []byte(leagueWithPositionsXML)))

	q.On("UpsertYahooLeague", mock.Anything, mock.Anything).Return(assert.AnError)

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	s.env.RegisterActivity(a.ImportYahooLeague)
	_, err := s.env.ExecuteActivity(a.ImportYahooLeague, ImportYahooLeagueInput{Season: 2023, LeagueID: 12345})

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "upsert league")
}

// ────────────────────────────────────────────────────────────────────────────
// ImportYahooTeams tests
// ────────────────────────────────────────────────────────────────────────────

type ImportYahooTeamsSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportYahooTeamsSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportYahooTeamsSuite(t *testing.T) {
	suite.Run(t, new(ImportYahooTeamsSuite))
}

func (s *ImportYahooTeamsSuite) TestEmptyTeams_ReturnsEmpty() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	s.env.RegisterActivity(a.ImportYahooTeams)
	val, err := s.env.ExecuteActivity(a.ImportYahooTeams, ImportYahooTeamsInput{Season: 2023, Teams: nil})

	require.NoError(s.T(), err)

	var result ImportYahooTeamsResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), 0, result.TeamsImported)
	assert.Equal(s.T(), 0, result.ManagersImported)
	q.AssertNotCalled(s.T(), "UpsertYahooTeamBatch")
}

func (s *ImportYahooTeamsSuite) TestMissingFile_ReturnsActionableError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	teams := []TeamInfo{{LeagueID: 12345, TeamID: 1}}
	s.env.RegisterActivity(a.ImportYahooTeams)
	_, err := s.env.ExecuteActivity(a.ImportYahooTeams, ImportYahooTeamsInput{Season: 2023, Teams: teams})

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "required Yahoo team cache is missing")
	q.AssertNotCalled(s.T(), "UpsertYahooTeamBatch")
}

func (s *ImportYahooTeamsSuite) TestWithTeamAndManager() {
	mem := store.NewMemStorage()
	q := &MockQueries{}

	teamRes := resource.Team{Season: 2023, LeagueID: 12345, TeamID: 1}
	require.NoError(s.T(), mem.Write(context.Background(), teamRes.Path(), []byte(teamWithManagerXML)))

	q.On("UpsertYahooTeamBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooTeamBatchBatchResults(&mockBatchResults{}, 1))
	q.On("UpsertYahooTeamManagerBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooTeamManagerBatchBatchResults(&mockBatchResults{}, 1))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	teams := []TeamInfo{{LeagueID: 12345, TeamID: 1}}

	s.env.RegisterActivity(a.ImportYahooTeams)
	val, err := s.env.ExecuteActivity(a.ImportYahooTeams, ImportYahooTeamsInput{Season: 2023, Teams: teams})

	require.NoError(s.T(), err)

	var result ImportYahooTeamsResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), 1, result.TeamsImported)
	assert.Equal(s.T(), 1, result.ManagersImported)
	q.AssertExpectations(s.T())
}

func (s *ImportYahooTeamsSuite) TestTeamUpsertError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}

	teamRes := resource.Team{Season: 2023, LeagueID: 12345, TeamID: 1}
	require.NoError(s.T(), mem.Write(context.Background(), teamRes.Path(), []byte(teamWithManagerXML)))

	q.On("UpsertYahooTeamBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooTeamBatchBatchResults(&mockBatchResults{execErr: assert.AnError}, 1))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	teams := []TeamInfo{{LeagueID: 12345, TeamID: 1}}

	s.env.RegisterActivity(a.ImportYahooTeams)
	_, err := s.env.ExecuteActivity(a.ImportYahooTeams, ImportYahooTeamsInput{Season: 2023, Teams: teams})

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "team 1")
}

// ────────────────────────────────────────────────────────────────────────────
// ImportYahooDataForDate tests
// ────────────────────────────────────────────────────────────────────────────

type ImportYahooDataForDateSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportYahooDataForDateSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportYahooDataForDateSuite(t *testing.T) {
	suite.Run(t, new(ImportYahooDataForDateSuite))
}

func (s *ImportYahooDataForDateSuite) TestEmptyTeams_ReturnsEmpty() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	input := ImportYahooDataForDateInput{
		Season: 2023,
		Teams:  nil,
		Date:   time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
	}

	s.env.RegisterActivity(a.ImportYahooDataForDate)
	val, err := s.env.ExecuteActivity(a.ImportYahooDataForDate, input)

	require.NoError(s.T(), err)

	var result ImportYahooDataForDateResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), 0, result.SummariesImported)
	assert.Equal(s.T(), 0, result.RostersImported)
	q.AssertNotCalled(s.T(), "UpsertYahooTeamSummaryBatch")
}

func (s *ImportYahooDataForDateSuite) TestNoFiles_ReturnsEmpty() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	input := ImportYahooDataForDateInput{
		Season: 2023,
		Teams:  []TeamInfo{{LeagueID: 12345, TeamID: 1}},
		Date:   time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
	}

	s.env.RegisterActivity(a.ImportYahooDataForDate)
	val, err := s.env.ExecuteActivity(a.ImportYahooDataForDate, input)

	require.NoError(s.T(), err)

	var result ImportYahooDataForDateResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), 0, result.SummariesImported)
	assert.Equal(s.T(), 0, result.RostersImported)
}

func (s *ImportYahooDataForDateSuite) TestWithSummaryAndRoster() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	teams := []TeamInfo{{LeagueID: 12345, TeamID: 1}}

	summaryRes := resource.TeamSummary{LeagueID: 12345, TeamID: 1, Date: date}
	rosterRes := resource.Roster{LeagueID: 12345, TeamID: 1, Date: date}
	require.NoError(s.T(), mem.Write(context.Background(), summaryRes.Path(), []byte(summaryWithStatsXML)))
	require.NoError(s.T(), mem.Write(context.Background(), rosterRes.Path(), []byte(rosterWithPlayersXML)))

	q.On("UpsertYahooTeamSummaryBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooTeamSummaryBatchBatchResults(&mockBatchResults{}, 1))
	q.On("UpsertYahooTeamRosterBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooTeamRosterBatchBatchResults(&mockBatchResults{}, 1))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	input := ImportYahooDataForDateInput{
		Season: 2023,
		Teams:  teams,
		Date:   date,
	}

	s.env.RegisterActivity(a.ImportYahooDataForDate)
	val, err := s.env.ExecuteActivity(a.ImportYahooDataForDate, input)

	require.NoError(s.T(), err)

	var result ImportYahooDataForDateResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), 1, result.SummariesImported)
	assert.Equal(s.T(), 1, result.RostersImported)
	q.AssertExpectations(s.T())
}

func (s *ImportYahooDataForDateSuite) TestSummaryUpsertError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	teams := []TeamInfo{{LeagueID: 12345, TeamID: 1}}

	summaryRes := resource.TeamSummary{LeagueID: 12345, TeamID: 1, Date: date}
	require.NoError(s.T(), mem.Write(context.Background(), summaryRes.Path(), []byte(summaryWithStatsXML)))

	q.On("UpsertYahooTeamSummaryBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooTeamSummaryBatchBatchResults(&mockBatchResults{execErr: assert.AnError}, 1))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	input := ImportYahooDataForDateInput{Season: 2023, Teams: teams, Date: date}

	s.env.RegisterActivity(a.ImportYahooDataForDate)
	_, err := s.env.ExecuteActivity(a.ImportYahooDataForDate, input)

	require.Error(s.T(), err)
}

// ────────────────────────────────────────────────────────────────────────────
// upsertSummaries / upsertRosters (direct calls)
// ────────────────────────────────────────────────────────────────────────────

func TestUpsertSummaries_Success(t *testing.T) {
	t.Parallel()

	q := &MockQueries{}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	params := []sqlcdb.UpsertYahooTeamSummaryBatchParams{
		{LeagueID: 12345, TeamID: 1, Date: pgDate(date), CoverageType: "date"},
	}

	q.On("UpsertYahooTeamSummaryBatch", mock.Anything, params).
		Return(sqlcdb.NewUpsertYahooTeamSummaryBatchBatchResults(&mockBatchResults{}, 1))

	err := upsertSummaries(context.Background(), q, params)
	require.NoError(t, err)
	q.AssertExpectations(t)
}

func TestUpsertSummaries_Error(t *testing.T) {
	t.Parallel()

	q := &MockQueries{}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	params := []sqlcdb.UpsertYahooTeamSummaryBatchParams{
		{LeagueID: 12345, TeamID: 1, Date: pgDate(date), CoverageType: "date"},
	}

	q.On("UpsertYahooTeamSummaryBatch", mock.Anything, params).
		Return(sqlcdb.NewUpsertYahooTeamSummaryBatchBatchResults(&mockBatchResults{execErr: assert.AnError}, 1))

	err := upsertSummaries(context.Background(), q, params)
	require.Error(t, err)
}

func TestUpsertRosters_Success(t *testing.T) {
	t.Parallel()

	q := &MockQueries{}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	params := []sqlcdb.UpsertYahooTeamRosterBatchParams{
		{LeagueID: 12345, TeamID: 1, Date: pgDate(date), PlayerID: 6616, SelectedPosition: "C"},
	}

	q.On("UpsertYahooTeamRosterBatch", mock.Anything, params).
		Return(sqlcdb.NewUpsertYahooTeamRosterBatchBatchResults(&mockBatchResults{}, 1))

	err := upsertRosters(context.Background(), q, params)
	require.NoError(t, err)
	q.AssertExpectations(t)
}

func TestUpsertRosters_Error(t *testing.T) {
	t.Parallel()

	q := &MockQueries{}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	params := []sqlcdb.UpsertYahooTeamRosterBatchParams{
		{LeagueID: 12345, TeamID: 1, Date: pgDate(date), PlayerID: 6616, SelectedPosition: "C"},
	}

	q.On("UpsertYahooTeamRosterBatch", mock.Anything, params).
		Return(sqlcdb.NewUpsertYahooTeamRosterBatchBatchResults(&mockBatchResults{execErr: assert.AnError}, 1))

	err := upsertRosters(context.Background(), q, params)
	require.Error(t, err)
}

// ────────────────────────────────────────────────────────────────────────────
// ImportYahooLeagueData tests
// ────────────────────────────────────────────────────────────────────────────

type ImportYahooLeagueDataSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportYahooLeagueDataSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportYahooLeagueDataSuite(t *testing.T) {
	suite.Run(t, new(ImportYahooLeagueDataSuite))
}

func (s *ImportYahooLeagueDataSuite) TestNoFiles_ReturnsZero() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	s.env.RegisterActivity(a.ImportYahooLeagueData)
	val, err := s.env.ExecuteActivity(a.ImportYahooLeagueData,
		ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})

	require.NoError(s.T(), err)

	var result ImportYahooLeagueDataResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), 0, result.TransactionsImported)
	assert.Equal(s.T(), 0, result.DraftPicksImported)
	assert.Equal(s.T(), 0, result.MatchupsImported)
	assert.ElementsMatch(s.T(), []string{"transactions", "draft results", "matchups"}, result.UnavailableResources)
	q.AssertNotCalled(s.T(), "UpsertYahooTransactionBatch")
}

func (s *ImportYahooLeagueDataSuite) TestWithTransactionsDraftMatchups() {
	mem := store.NewMemStorage()
	q := &MockQueries{}

	txRes := resource.Transactions{Season: 2023, LeagueID: 12345}
	drRes := resource.DraftResults{Season: 2023, LeagueID: 12345}
	mu1Res := resource.Matchups{Season: 2023, LeagueID: 12345, Week: 1}
	require.NoError(s.T(), mem.Write(context.Background(), txRes.Path(), []byte(transactionsWithOneXML)))
	require.NoError(s.T(), mem.Write(context.Background(), drRes.Path(), []byte(draftResultsWithPicksXML)))
	require.NoError(s.T(), mem.Write(context.Background(), mu1Res.Path(), []byte(matchupsWeek1XML)))

	q.On("UpsertYahooTransactionBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooTransactionBatchBatchResults(&mockBatchResults{}, 1))
	q.On("UpsertYahooDraftResultBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooDraftResultBatchBatchResults(&mockBatchResults{}, 2))
	q.On("UpsertYahooMatchupBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooMatchupBatchBatchResults(&mockBatchResults{}, 1))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	s.env.RegisterActivity(a.ImportYahooLeagueData)
	val, err := s.env.ExecuteActivity(a.ImportYahooLeagueData,
		ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})

	require.NoError(s.T(), err)

	var result ImportYahooLeagueDataResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), 1, result.TransactionsImported)
	assert.Equal(s.T(), 2, result.DraftPicksImported)
	assert.Equal(s.T(), 1, result.MatchupsImported)
	q.AssertExpectations(s.T())
}

func (s *ImportYahooLeagueDataSuite) TestTransactionUpsertError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}

	txRes := resource.Transactions{Season: 2023, LeagueID: 12345}
	require.NoError(s.T(), mem.Write(context.Background(), txRes.Path(), []byte(transactionsWithOneXML)))

	q.On("UpsertYahooTransactionBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooTransactionBatchBatchResults(&mockBatchResults{execErr: assert.AnError}, 1))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	s.env.RegisterActivity(a.ImportYahooLeagueData)
	_, err := s.env.ExecuteActivity(a.ImportYahooLeagueData,
		ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "import transactions")
}

func (s *ImportYahooLeagueDataSuite) TestMatchupUpsertError() {
	mem := store.NewMemStorage()
	q := &MockQueries{}

	txRes := resource.Transactions{Season: 2023, LeagueID: 12345}
	drRes := resource.DraftResults{Season: 2023, LeagueID: 12345}
	mu1Res := resource.Matchups{Season: 2023, LeagueID: 12345, Week: 1}
	require.NoError(s.T(), mem.Write(context.Background(), txRes.Path(),
		[]byte(`<fantasy_content><league><transactions count="0"></transactions></league></fantasy_content>`)))
	require.NoError(s.T(), mem.Write(context.Background(), drRes.Path(),
		[]byte(`<fantasy_content><league><draft_results count="0"></draft_results></league></fantasy_content>`)))
	require.NoError(s.T(), mem.Write(context.Background(), mu1Res.Path(), []byte(matchupsWeek1XML)))

	q.On("UpsertYahooMatchupBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooMatchupBatchBatchResults(&mockBatchResults{execErr: assert.AnError}, 1))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	s.env.RegisterActivity(a.ImportYahooLeagueData)
	_, err := s.env.ExecuteActivity(a.ImportYahooLeagueData,
		ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "import matchups")
}

// TestNonContiguousCache_ImportsLaterWeeks verifies that a gap in the cached
// weeks (week 1 present, week 2 absent, week 3 present) does not stop the import
// at the gap. The loop must continue past the missing week and still import the
// later week, matching the fetcher's continue-on-skip semantics.
func TestImportYahooMatchups_NonContiguousCache_ImportsLaterWeeks(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	q := &MockQueries{}

	// Week 1 and week 3 cached; week 2 deliberately absent (gap).
	mu1Res := resource.Matchups{Season: 2023, LeagueID: 12345, Week: 1}
	mu3Res := resource.Matchups{Season: 2023, LeagueID: 12345, Week: 3}
	require.NoError(t, mem.Write(context.Background(), mu1Res.Path(), []byte(matchupsWeek1XML)))
	require.NoError(t, mem.Write(context.Background(), mu3Res.Path(), []byte(matchupsWeek3XML)))

	// One matchup per cached week → each batch reports one row.
	q.On("UpsertYahooMatchupBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooMatchupBatchBatchResults(&mockBatchResults{}, 1))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	count, err := a.importYahooMatchups(context.Background(),
		ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})

	require.NoError(t, err)
	// Both weeks import despite the week-2 gap; a break-on-absent loop would return 1.
	assert.Equal(t, 2, count)
	q.AssertNumberOfCalls(t, "UpsertYahooMatchupBatch", 2)
}

// ────────────────────────────────────────────────────────────────────────────
// collectSummaryParams — invalid stat ID is skipped
// ────────────────────────────────────────────────────────────────────────────

func TestCollectSummaryParams_InvalidStatID_Skipped(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	teams := []TeamInfo{{LeagueID: 12345, TeamID: 1}}

	xml := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <team>
    <team_stats>
      <coverage_type>date</coverage_type>
      <stats>
        <stat><stat_id>not-a-number</stat_id><value>5</value></stat>
        <stat><stat_id>2</stat_id><value>10</value></stat>
      </stats>
    </team_stats>
  </team>
</fantasy_content>`)
	res := resource.TeamSummary{LeagueID: 12345, TeamID: 1, Date: date}
	require.NoError(t, mem.Write(context.Background(), res.Path(), xml))

	summaryParams := a.collectSummaryParams(
		context.Background(), teams, date, make(core.OriginCounts),
	)

	// The summary should have the valid stat (2=assists) populated, invalid stat ID skipped.
	require.Len(t, summaryParams, 1)
	assert.True(t, summaryParams[0].Assists.Valid)
	assert.Equal(t, float32(10), summaryParams[0].Assists.Float32)
	assert.False(t, summaryParams[0].Goals.Valid) // stat_id "not-a-number" was skipped
}

// ────────────────────────────────────────────────────────────────────────────
// helpers
// ────────────────────────────────────────────────────────────────────────────

// pgDate constructs a pgtype.Date for use in test parameter construction.
func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}
