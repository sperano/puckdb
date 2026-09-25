package yahoo

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// secondPageXML is a synthetic second page of the points league pool that
// re-lists player 7001 (must be deduped, first page wins) and adds a new
// player 7008.
const secondPageXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <league_key>465.l.77777</league_key>
    <league_id>77777</league_id>
    <players count="2">
      <player>
        <player_key>465.p.7001</player_key>
        <player_id>7001</player_id>
        <name><full>Alex Twoway Duplicate</full></name>
        <editorial_team_abbr>TOR</editorial_team_abbr>
        <display_position>C,LW</display_position>
        <position_type>P</position_type>
        <primary_position>C</primary_position>
        <eligible_positions><position>C</position></eligible_positions>
      </player>
      <player>
        <player_key>465.p.7008</player_key>
        <player_id>7008</player_id>
        <name><full>Hank Extra</full></name>
        <editorial_team_abbr>VAN</editorial_team_abbr>
        <display_position>D</display_position>
        <position_type>P</position_type>
        <primary_position>D</primary_position>
        <eligible_positions><position>D</position></eligible_positions>
      </player>
    </players>
  </league>
</fantasy_content>`

// wrongGameKeyPageXML lists a player whose key carries a game key that does
// not match the manifest's game key.
const wrongGameKeyPageXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <league_key>465.l.77777</league_key>
    <league_id>77777</league_id>
    <players count="1">
      <player>
        <player_key>453.p.7001</player_key>
        <player_id>7001</player_id>
        <name><full>Wrong Game</full></name>
        <eligible_positions><position>C</position></eligible_positions>
      </player>
    </players>
  </league>
</fantasy_content>`

// otherLeaguePageXML has the manifest's numeric league ID (so resource.Parse
// accepts it) but a league_key naming a different game, which
// collectPoolPlayers must still reject: the manifest is for "465.l.77777".
const otherLeaguePageXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <league_key>453.l.77777</league_key>
    <league_id>77777</league_id>
    <players count="1">
      <player>
        <player_key>453.p.7001</player_key>
        <player_id>7001</player_id>
        <name><full>Other League</full></name>
        <eligible_positions><position>C</position></eligible_positions>
      </player>
    </players>
  </league>
</fantasy_content>`

// emptyPageXML lists zero players.
const emptyPageXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <league_key>465.l.77777</league_key>
    <league_id>77777</league_id>
    <players count="0"></players>
  </league>
</fantasy_content>`

// committedDownloadID is the download the manifest names;
// uncommittedDownloadID names a later download no manifest refers to.
const (
	committedDownloadID   = int64(1758369600000)
	uncommittedDownloadID = int64(1758456000000)
)

var importPlayersFetchedAt = time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC)

func writePlayersPage(t *testing.T, mem *store.MemStorage, start int, xml []byte) {
	t.Helper()
	page := resource.LeaguePlayers{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, Start: start}
	require.NoError(t, mem.Write(context.Background(), page.Path(), xml))
}

func writePoolManifest(t *testing.T, mem *store.MemStorage, starts []int, players int) {
	t.Helper()
	manifestRes := resource.LeaguePlayerPool{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, manifestRes, resource.LeaguePlayerPoolManifest{
		LeagueKey: "465.l.77777", GameKey: yahoofixtures.PointsGameKey, FetchedAt: importPlayersFetchedAt,
		Starts: starts, Players: players,
	}))
}

func runImportPlayersActivityFor(t *testing.T, a *ImportActivities, season, leagueID int) (ImportYahooLeaguePlayersResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.ImportYahooLeaguePlayers)
	val, err := env.ExecuteActivity(a.ImportYahooLeaguePlayers,
		ImportYahooLeaguePlayersInput{Season: season, LeagueID: leagueID})
	var result ImportYahooLeaguePlayersResult
	if err == nil {
		require.NoError(t, val.Get(&result))
	}
	return result, err
}

func runImportPlayersActivity(t *testing.T, a *ImportActivities) (ImportYahooLeaguePlayersResult, error) {
	t.Helper()
	return runImportPlayersActivityFor(t, a, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID)
}

func findPlayerParam(t *testing.T, params []sqlcdb.UpsertYahooLeaguePlayerBatchParams, id int32) sqlcdb.UpsertYahooLeaguePlayerBatchParams {
	t.Helper()
	for _, p := range params {
		if p.PlayerID == id {
			return p
		}
	}
	t.Fatalf("no captured params for player %d", id)
	return sqlcdb.UpsertYahooLeaguePlayerBatchParams{}
}

func TestImportYahooLeaguePlayers_HappyPath(t *testing.T) {
	mem := store.NewMemStorage()
	writePlayersPage(t, mem, 0, yahoofixtures.Read(yahoofixtures.PlayersPage))
	writePoolManifest(t, mem, []int{0}, yahoofixtures.PlayersInPage)

	q := &MockQueries{}
	var captured []sqlcdb.UpsertYahooLeaguePlayerBatchParams
	q.On("UpsertYahooLeaguePlayerBatch", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).([]sqlcdb.UpsertYahooLeaguePlayerBatchParams)
		}).
		Return(sqlcdb.NewUpsertYahooLeaguePlayerBatchBatchResults(&mockBatchResults{}, yahoofixtures.PlayersInPage))
	q.On("DeleteStaleYahooLeaguePlayers", mock.Anything, mock.MatchedBy(func(p sqlcdb.DeleteStaleYahooLeaguePlayersParams) bool {
		return p.LeagueKey == "465.l.77777" && p.FetchedAt.Time.Equal(importPlayersFetchedAt)
	})).Return(int64(1), nil)

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	result, err := runImportPlayersActivity(t, a)

	require.NoError(t, err)
	assert.Equal(t, ImportYahooLeaguePlayersResult{Players: yahoofixtures.PlayersInPage, Removed: 1}, result)
	require.Len(t, captured, yahoofixtures.PlayersInPage)

	p7001 := findPlayerParam(t, captured, 7001)
	assert.Equal(t, []string{"C", "LW", "Util"}, p7001.EligiblePositions)
	assert.Equal(t, int32(yahoofixtures.PointsGameKey), p7001.GameKey)
	assert.Equal(t, "465.l.77777", p7001.LeagueKey)
	assert.True(t, p7001.FetchedAt.Valid)
	assert.True(t, p7001.FetchedAt.Time.Equal(importPlayersFetchedAt))

	p7004 := findPlayerParam(t, captured, 7004)
	assert.Equal(t, "IR", p7004.Status)
	assert.Equal(t, "Injured Reserve", p7004.StatusFull)
	assert.Equal(t, "Upper Body", p7004.InjuryNote)
	assert.True(t, p7004.OnDisabledList)

	p7006 := findPlayerParam(t, captured, 7006)
	assert.NotNil(t, p7006.EligiblePositions)
	assert.Empty(t, p7006.EligiblePositions)

	q.AssertExpectations(t)
}

func TestImportYahooLeaguePlayers_DuplicateAcrossPagesImportedOnce(t *testing.T) {
	mem := store.NewMemStorage()
	writePlayersPage(t, mem, 0, yahoofixtures.Read(yahoofixtures.PlayersPage))
	writePlayersPage(t, mem, 25, []byte(secondPageXML))
	writePoolManifest(t, mem, []int{0, 25}, yahoofixtures.PlayersInPage+1)

	q := &MockQueries{}
	var captured []sqlcdb.UpsertYahooLeaguePlayerBatchParams
	q.On("UpsertYahooLeaguePlayerBatch", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).([]sqlcdb.UpsertYahooLeaguePlayerBatchParams)
		}).
		Return(sqlcdb.NewUpsertYahooLeaguePlayerBatchBatchResults(&mockBatchResults{}, yahoofixtures.PlayersInPage+1))
	q.On("DeleteStaleYahooLeaguePlayers", mock.Anything, mock.Anything).Return(int64(0), nil)

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	result, err := runImportPlayersActivity(t, a)

	require.NoError(t, err)
	// 7 players on page one plus one new player (7008) on page two; the
	// re-listed player 7001 must not be counted twice.
	assert.Equal(t, yahoofixtures.PlayersInPage+1, result.Players)
	firstPageName := findPlayerParam(t, captured, 7001).FullName
	assert.Equal(t, "Alex Twoway", firstPageName, "the first page's copy of a duplicated player must win")
}

func TestImportYahooLeaguePlayers_PageLeagueKeyMismatch_NonRetryable(t *testing.T) {
	mem := store.NewMemStorage()
	writePlayersPage(t, mem, 0, []byte(otherLeaguePageXML))
	writePoolManifest(t, mem, []int{0}, 1)
	q := &MockQueries{}

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	_, err := runImportPlayersActivity(t, a)

	require.Error(t, err)
	assert.True(t, isNonRetryable(err))
	q.AssertNotCalled(t, "UpsertYahooLeaguePlayerBatch", mock.Anything, mock.Anything)
}

func TestImportYahooLeaguePlayers_PlayerKeyWrongGameKey_NonRetryable(t *testing.T) {
	mem := store.NewMemStorage()
	writePlayersPage(t, mem, 0, []byte(wrongGameKeyPageXML))
	writePoolManifest(t, mem, []int{0}, 1)
	q := &MockQueries{}

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	_, err := runImportPlayersActivity(t, a)

	require.Error(t, err)
	assert.True(t, isNonRetryable(err))
	q.AssertNotCalled(t, "UpsertYahooLeaguePlayerBatch", mock.Anything, mock.Anything)
}

func TestImportYahooLeaguePlayers_MissingManifest_CurrentLeague_Unavailable(t *testing.T) {
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, yahoofixtures.Read(yahoofixtures.PointsLeague))
	q := &MockQueries{}

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	result, err := runImportPlayersActivity(t, a)

	require.NoError(t, err)
	assert.True(t, result.Unavailable)
	q.AssertNotCalled(t, "UpsertYahooLeaguePlayerBatch", mock.Anything, mock.Anything)
	q.AssertNotCalled(t, "DeleteStaleYahooLeaguePlayers", mock.Anything, mock.Anything)
}

func TestImportYahooLeaguePlayers_MissingManifest_EndedLeague_NotUnavailable(t *testing.T) {
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID, yahoofixtures.Read(yahoofixtures.RotoLeague))
	q := &MockQueries{}

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	result, err := runImportPlayersActivityFor(t, a, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID)

	require.NoError(t, err)
	assert.False(t, result.Unavailable)
	q.AssertNotCalled(t, "UpsertYahooLeaguePlayerBatch", mock.Anything, mock.Anything)
	q.AssertNotCalled(t, "DeleteStaleYahooLeaguePlayers", mock.Anything, mock.Anything)
}

func TestImportYahooLeaguePlayers_EmptySnapshot_Unavailable(t *testing.T) {
	mem := store.NewMemStorage()
	writePlayersPage(t, mem, 0, []byte(emptyPageXML))
	writePoolManifest(t, mem, []int{0}, 0)
	q := &MockQueries{}

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	result, err := runImportPlayersActivity(t, a)

	require.NoError(t, err)
	assert.True(t, result.Unavailable)
	q.AssertNotCalled(t, "UpsertYahooLeaguePlayerBatch", mock.Anything, mock.Anything)
	q.AssertNotCalled(t, "DeleteStaleYahooLeaguePlayers", mock.Anything, mock.Anything)
}

func TestImportYahooLeaguePlayers_UpsertBatchError_NoDelete(t *testing.T) {
	mem := store.NewMemStorage()
	writePlayersPage(t, mem, 0, yahoofixtures.Read(yahoofixtures.PlayersPage))
	writePoolManifest(t, mem, []int{0}, yahoofixtures.PlayersInPage)

	q := &MockQueries{}
	q.On("UpsertYahooLeaguePlayerBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooLeaguePlayerBatchBatchResults(&mockBatchResults{execErr: assert.AnError}, yahoofixtures.PlayersInPage))

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
	_, err := runImportPlayersActivity(t, a)

	require.Error(t, err)
	q.AssertNotCalled(t, "DeleteStaleYahooLeaguePlayers", mock.Anything, mock.Anything)
}

// Pages of a later download that never committed live in their own
// directory and must not leak into the committed snapshot.
func TestImportYahooLeaguePlayers_IgnoresUncommittedDownload(t *testing.T) {
	mem := store.NewMemStorage()
	pagePath := func(downloadID int64) string {
		return resource.LeaguePlayers{
			Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, DownloadID: downloadID,
		}.Path()
	}
	mem.SetFile(pagePath(committedDownloadID), yahoofixtures.Read(yahoofixtures.PlayersPage))
	mem.SetFile(pagePath(uncommittedDownloadID), []byte(emptyPageXML))
	manifestRes := resource.LeaguePlayerPool{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, manifestRes, resource.LeaguePlayerPoolManifest{
		DownloadID: committedDownloadID, LeagueKey: "465.l.77777", GameKey: yahoofixtures.PointsGameKey,
		FetchedAt: importPlayersFetchedAt, Starts: []int{0}, Players: yahoofixtures.PlayersInPage,
	}))

	q := &MockQueries{}
	q.On("UpsertYahooLeaguePlayerBatch", mock.Anything,
		mock.MatchedBy(func(p []sqlcdb.UpsertYahooLeaguePlayerBatchParams) bool { return len(p) == yahoofixtures.PlayersInPage })).
		Return(sqlcdb.NewUpsertYahooLeaguePlayerBatchBatchResults(&mockBatchResults{}, yahoofixtures.PlayersInPage))
	q.On("DeleteStaleYahooLeaguePlayers", mock.Anything, mock.Anything).Return(int64(0), nil)

	result, err := runImportPlayersActivity(t, &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q})

	require.NoError(t, err)
	assert.Equal(t, yahoofixtures.PlayersInPage, result.Players)
	q.AssertExpectations(t)
}
