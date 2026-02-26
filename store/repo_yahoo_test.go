package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sample Yahoo Fantasy XML for testing.
// Note: XML element names differ from Go struct field names (see xml_yahoo.go).
const testLeagueXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xmlns="http://fantasysports.yahooapis.com/fantasy/v2/base.rng">
  <league>
    <league_key>411.l.12345</league_key>
    <league_id>12345</league_id>
    <name>Test League</name>
  </league>
</fantasy_content>`

const testTeamXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xmlns="http://fantasysports.yahooapis.com/fantasy/v2/base.rng">
  <team>
    <team_key>411.l.12345.t.1</team_key>
    <team_id>1</team_id>
    <name>Test Team</name>
  </team>
</fantasy_content>`

const testRosterXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xmlns="http://fantasysports.yahooapis.com/fantasy/v2/base.rng">
  <team>
    <roster>
      <coverage_type>date</coverage_type>
    </roster>
  </team>
</fantasy_content>`

const testGameKeyXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xmlns="http://fantasysports.yahooapis.com/fantasy/v2/base.rng">
  <game>
    <game_key>411</game_key>
    <game_id>411</game_id>
    <name>Hockey</name>
    <season>2023</season>
  </game>
</fantasy_content>`

// ============================================================================
// LEAGUE TESTS
// ============================================================================

func TestYahooRepo_SaveAndGetLeague(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	leagueID := 12345
	data := []byte(testLeagueXML)

	err := repo.SaveLeague(season, leagueID, data)
	require.NoError(t, err)

	assert.True(t, repo.LeagueExists(season, leagueID))

	content, err := repo.GetLeague(season, leagueID)
	require.NoError(t, err)
	assert.NotNil(t, content)
	assert.Equal(t, 12345, content.League.ID)
	assert.Equal(t, "Test League", content.League.Name)
}

func TestYahooRepo_GetLeagueRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	leagueID := 12345
	data := []byte(testLeagueXML)

	repo.SaveLeague(season, leagueID, data)

	got, err := repo.GetLeagueRaw(season, leagueID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestYahooRepo_GetLeagueInvalidXML(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	leagueID := 12345
	storage.SetFile(LeaguePath(season, leagueID), []byte(`not valid xml`))

	_, err := repo.GetLeague(season, leagueID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse league")
}

func TestYahooRepo_LeagueExists(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	leagueID := 12345

	assert.False(t, repo.LeagueExists(season, leagueID))

	repo.SaveLeague(season, leagueID, []byte(testLeagueXML))
	assert.True(t, repo.LeagueExists(season, leagueID))
}

// ============================================================================
// TEAM TESTS
// ============================================================================

func TestYahooRepo_SaveAndGetTeam(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	leagueID := 12345
	teamID := 1
	data := []byte(testTeamXML)

	err := repo.SaveTeam(season, leagueID, teamID, data)
	require.NoError(t, err)

	assert.True(t, repo.TeamExists(season, leagueID, teamID))

	content, err := repo.GetTeam(season, leagueID, teamID)
	require.NoError(t, err)
	assert.NotNil(t, content)
	assert.Equal(t, 1, content.Team.ID)
	assert.Equal(t, "Test Team", content.Team.Name)
}

func TestYahooRepo_GetTeamRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	leagueID := 12345
	teamID := 1
	data := []byte(testTeamXML)

	repo.SaveTeam(season, leagueID, teamID, data)

	got, err := repo.GetTeamRaw(season, leagueID, teamID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestYahooRepo_GetTeamInvalidXML(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	leagueID := 12345
	teamID := 1
	storage.SetFile(TeamPath(season, leagueID, teamID), []byte(`not valid xml`))

	_, err := repo.GetTeam(season, leagueID, teamID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse team")
}

// ============================================================================
// ROSTER TESTS
// ============================================================================

func TestYahooRepo_SaveAndGetRoster(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	leagueID := 12345
	teamID := 1
	date := time.Date(2023, 12, 15, 0, 0, 0, 0, time.UTC)
	data := []byte(testRosterXML)

	err := repo.SaveRoster(leagueID, teamID, date, data)
	require.NoError(t, err)

	assert.True(t, repo.RosterExists(leagueID, teamID, date))

	content, err := repo.GetRoster(leagueID, teamID, date)
	require.NoError(t, err)
	assert.NotNil(t, content)
}

func TestYahooRepo_GetRosterRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	leagueID := 12345
	teamID := 1
	date := time.Date(2023, 12, 15, 0, 0, 0, 0, time.UTC)
	data := []byte(testRosterXML)

	repo.SaveRoster(leagueID, teamID, date, data)

	got, err := repo.GetRosterRaw(leagueID, teamID, date)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestYahooRepo_GetRosterInvalidXML(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	leagueID := 12345
	teamID := 1
	date := time.Date(2023, 12, 15, 0, 0, 0, 0, time.UTC)
	storage.SetFile(RosterPath(leagueID, teamID, date), []byte(`not valid xml`))

	_, err := repo.GetRoster(leagueID, teamID, date)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse roster")
}

// ============================================================================
// TEAM SUMMARY TESTS
// ============================================================================

func TestYahooRepo_SaveAndGetTeamSummary(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	leagueID := 12345
	teamID := 1
	date := time.Date(2023, 12, 15, 0, 0, 0, 0, time.UTC)
	data := []byte(testTeamXML) // reuse team XML

	err := repo.SaveTeamSummary(leagueID, teamID, date, data)
	require.NoError(t, err)

	assert.True(t, repo.TeamSummaryExists(leagueID, teamID, date))

	content, err := repo.GetTeamSummary(leagueID, teamID, date)
	require.NoError(t, err)
	assert.NotNil(t, content)
}

func TestYahooRepo_GetTeamSummaryRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	leagueID := 12345
	teamID := 1
	date := time.Date(2023, 12, 15, 0, 0, 0, 0, time.UTC)
	data := []byte(testTeamXML)

	repo.SaveTeamSummary(leagueID, teamID, date, data)

	got, err := repo.GetTeamSummaryRaw(leagueID, teamID, date)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestYahooRepo_GetTeamSummaryInvalidXML(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	leagueID := 12345
	teamID := 1
	date := time.Date(2023, 12, 15, 0, 0, 0, 0, time.UTC)
	storage.SetFile(TeamSummaryPath(leagueID, teamID, date), []byte(`not valid xml`))

	_, err := repo.GetTeamSummary(leagueID, teamID, date)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse team summary")
}

// ============================================================================
// YAHOO PLAYER TESTS (HTML)
// ============================================================================

func TestYahooRepo_SaveAndGetPlayer(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	playerID := YahooPlayerID(12345)
	data := []byte(`<html><body>Player Page</body></html>`)

	err := repo.SavePlayer(playerID, data)
	require.NoError(t, err)

	assert.True(t, repo.PlayerExists(playerID))

	got, err := repo.GetPlayer(playerID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestYahooRepo_PlayerMissing(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	playerID := YahooPlayerID(99999)

	assert.False(t, repo.IsPlayerMissing(playerID))

	err := repo.MarkPlayerMissing(playerID)
	require.NoError(t, err)

	assert.True(t, repo.IsPlayerMissing(playerID))
}

func TestYahooRepo_ListPlayers(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	// Save some players
	repo.SavePlayer(YahooPlayerID(100), []byte(`<html>100</html>`))
	repo.SavePlayer(YahooPlayerID(200), []byte(`<html>200</html>`))
	repo.SavePlayer(YahooPlayerID(300), []byte(`<html>300</html>`))

	players, err := repo.ListPlayers()
	require.NoError(t, err)
	assert.Len(t, players, 3)

	// Check all IDs are present (order may vary)
	ids := make(map[YahooPlayerID]bool)
	for _, id := range players {
		ids[id] = true
	}
	assert.True(t, ids[YahooPlayerID(100)])
	assert.True(t, ids[YahooPlayerID(200)])
	assert.True(t, ids[YahooPlayerID(300)])
}

func TestYahooRepo_ListPlayersEmpty(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	players, err := repo.ListPlayers()
	require.NoError(t, err)
	assert.Empty(t, players)
}

// ============================================================================
// GAME KEY TESTS
// ============================================================================

func TestYahooRepo_SaveAndGetGameKey(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	data := []byte(testGameKeyXML)

	err := repo.SaveGameKey(season, data)
	require.NoError(t, err)

	assert.True(t, repo.GameKeyExists(season))

	content, err := repo.GetGameKey(season)
	require.NoError(t, err)
	assert.NotNil(t, content)
	assert.Equal(t, 411, content.Game.Key)
	assert.Equal(t, "Hockey", content.Game.Name)
}

func TestYahooRepo_GetGameKeyRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	data := []byte(testGameKeyXML)

	repo.SaveGameKey(season, data)

	got, err := repo.GetGameKeyRaw(season)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestYahooRepo_GetGameKeyInvalidXML(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023
	storage.SetFile(GameKeyPath(season), []byte(`not valid xml`))

	_, err := repo.GetGameKey(season)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse game key")
}

func TestYahooRepo_GameKeyExists(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewYahooRepo(storage)

	season := 2023

	assert.False(t, repo.GameKeyExists(season))

	repo.SaveGameKey(season, []byte(testGameKeyXML))
	assert.True(t, repo.GameKeyExists(season))
}
