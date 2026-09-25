package yahoo

// PostgreSQL-backed tests of the league rules and player pool imports: the
// real queries, the real migrations, and the draft loaders reading back what
// the activities wrote. They skip unless PUCKDB_TEST_PG_URL names a test
// database (see CLAUDE.md "Database-backed tests").

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	envTestPGURL     = "PUCKDB_TEST_PG_URL"
	testDBNameMarker = "test"
	// pgTestNHLPlayerID is the NHL player mapped to fixture Yahoo player 7001.
	pgTestNHLPlayerID   = 8470001
	pgTestMappedYahooID = 7001
	pgTestStaleAfter    = 24 * time.Hour
	pgTestLaterGameKey  = 465
)

var pgTestFetchTime = time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)

var pgMigrateOnce struct {
	sync.Once
	err error
}

// openDraftTestDB migrates the test database once and empties the tables
// these tests use.
func openDraftTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL draft rules tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBNameMarker,
		"%s must name a dedicated test database (URL containing %q)", envTestPGURL, testDBNameMarker)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err, "migrate test database")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `TRUNCATE yahoo_leagues, yahoo_league_roster_positions, yahoo_league_stat_categories,
		yahoo_league_rule_snapshots, yahoo_league_players, yahoo_teams, players CASCADE`)
	require.NoError(t, err)
	return pool
}

type draftPGFixture struct {
	pool    *pgxpool.Pool
	storage *store.MemStorage
	acts    *ImportActivities
	env     *testsuite.TestActivityEnvironment
}

func newDraftPGFixture(t *testing.T) *draftPGFixture {
	pool := openDraftTestDB(t)
	mem := store.NewMemStorage()
	acts := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: sqlcdb.New(pool)}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.ImportYahooLeague)
	env.RegisterActivity(acts.ImportYahooStandInLeague)
	env.RegisterActivity(acts.ImportYahooLeaguePlayers)
	return &draftPGFixture{pool: pool, storage: mem, acts: acts, env: env}
}

func (f *draftPGFixture) cacheLeague(season, leagueID int, fixture string, fetched time.Time) {
	f.storage.SetFileWithTime(resource.League{Season: season, LeagueID: leagueID}.Path(), yahoofixtures.Read(fixture), fetched)
}

func (f *draftPGFixture) importLeague(t *testing.T, season, leagueID int) ImportYahooLeagueResult {
	t.Helper()
	val, err := f.env.ExecuteActivity(f.acts.ImportYahooLeague, ImportYahooLeagueInput{Season: season, LeagueID: leagueID})
	require.NoError(t, err)
	var result ImportYahooLeagueResult
	require.NoError(t, val.Get(&result))
	return result
}

func (f *draftPGFixture) loadReport(t *testing.T, season, leagueID int) draft.LeagueReport {
	t.Helper()
	report, err := draft.LoadLeagueReport(context.Background(), sqlcdb.New(f.pool), season, leagueID,
		pgTestFetchTime, pgTestStaleAfter)
	require.NoError(t, err)
	return report
}

func TestPg_RulesVersionsFollowSettingChanges(t *testing.T) {
	f := newDraftPGFixture(t)
	f.cacheLeague(yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID, yahoofixtures.RotoLeague, pgTestFetchTime)

	assert.True(t, f.importLeague(t, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID).NewRulesVersion)
	assert.False(t, f.importLeague(t, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID).NewRulesVersion,
		"re-importing identical settings must not create a version")
	first := f.loadReport(t, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID)
	assert.EqualValues(t, 1, first.Snapshot.Versions)
	assert.Equal(t, draft.SourceYahooAPI, first.Snapshot.Source)
	assert.Equal(t, yahoofixtures.RotoGameKey, first.Snapshot.Rules.GameKey)
	assert.True(t, first.Snapshot.FetchedAt.Equal(pgTestFetchTime))

	changedAt := pgTestFetchTime.Add(time.Hour)
	f.cacheLeague(yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID, yahoofixtures.RotoLeagueChanged, changedAt)
	assert.True(t, f.importLeague(t, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID).NewRulesVersion)

	latest := f.loadReport(t, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID)
	assert.EqualValues(t, 2, latest.Snapshot.Versions)
	assert.NotEqual(t, first.Snapshot.Hash, latest.Snapshot.Hash)
	_, hasHits := findCategory(latest.Snapshot.Rules, 31)
	assert.True(t, hasHits, "the changed version scores HIT")
	assert.True(t, latest.Snapshot.FetchedAt.Equal(changedAt))
}

func findCategory(rules draft.Rules, statID int) (draft.StatCategory, bool) {
	for _, c := range rules.Categories {
		if c.StatID == statID {
			return c, true
		}
	}
	return draft.StatCategory{}, false
}

func TestPg_StatCategoriesStoreDirectionWeightAndDisplayOnly(t *testing.T) {
	f := newDraftPGFixture(t)
	f.cacheLeague(yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, yahoofixtures.PointsLeague, pgTestFetchTime)
	f.importLeague(t, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID)

	rows, err := sqlcdb.New(f.pool).GetYahooLeagueStatCategories(context.Background(), yahoofixtures.PointsLeagueID)
	require.NoError(t, err)
	byID := make(map[int32]sqlcdb.YahooLeagueStatCategory)
	for _, row := range rows {
		byID[row.StatID] = row
	}
	goalsAgainst := byID[22]
	assert.True(t, goalsAgainst.Value.Valid)
	assert.InDelta(t, -2, goalsAgainst.Value.Float32, 0)
	assert.EqualValues(t, 0, goalsAgainst.SortOrder.Int16, "GA is lower-is-better")
	assert.Equal(t, "G", goalsAgainst.PositionType)
	savePct := byID[26]
	assert.True(t, savePct.IsOnlyDisplayStat)
	assert.False(t, savePct.Value.Valid, "display-only SV% has no points weight")
}

// A league ID that already holds another season's league must not be
// overwritten: the legacy tables key leagues by numeric ID only.
func TestPg_LeagueIDFromAnotherSeasonIsRefused(t *testing.T) {
	f := newDraftPGFixture(t)
	f.cacheLeague(yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID, yahoofixtures.RotoLeague, pgTestFetchTime)
	f.importLeague(t, yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID)

	laterSeason := yahoofixtures.RotoSeason + 1
	later := yahoofixtures.Read(yahoofixtures.RotoLeague)
	f.storage.SetFileWithTime(resource.League{Season: laterSeason, LeagueID: yahoofixtures.RotoLeagueID}.Path(),
		relabelSeason(later, laterSeason), pgTestFetchTime)
	_, err := f.env.ExecuteActivity(f.acts.ImportYahooLeague,
		ImportYahooLeagueInput{Season: laterSeason, LeagueID: yahoofixtures.RotoLeagueID})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides with season 2025 league 1003")
	stored, err := sqlcdb.New(f.pool).GetYahooLeague(context.Background(), yahoofixtures.RotoLeagueID)
	require.NoError(t, err)
	assert.EqualValues(t, yahoofixtures.RotoSeason, stored.Season, "the earlier season's row is untouched")
}

func TestPg_StandInSnapshotIsProvisional(t *testing.T) {
	f := newDraftPGFixture(t)
	f.cacheLeague(yahoofixtures.RotoSeason, yahoofixtures.RotoLeagueID, yahoofixtures.RotoLeague, pgTestFetchTime)
	input := ImportYahooStandInLeagueInput{
		Season: yahoofixtures.RotoSeason + 1, LeagueID: standInTestLeagueID,
		Source: config.LeagueMetadataSource{Season: yahoofixtures.RotoSeason, LeagueID: yahoofixtures.RotoLeagueID},
	}
	_, err := f.env.ExecuteActivity(f.acts.ImportYahooStandInLeague, input)
	require.NoError(t, err)

	report := f.loadReport(t, input.Season, input.LeagueID)
	assert.Equal(t, draft.SourceTemporaryStandIn, report.Snapshot.Source)
	assert.Equal(t, "453.l.1003", report.Snapshot.SourceLeagueKey)
	assert.Zero(t, report.Snapshot.Rules.GameKey)
	assert.Nil(t, report.OwnedTeam)
	scoring, err := draft.ScoringFor(report.Snapshot)
	require.NoError(t, err)
	assert.True(t, scoring.Provisional)
}

func TestPg_PlayerPoolReplacesSnapshotAndMapsNHLPlayers(t *testing.T) {
	f := newDraftPGFixture(t)
	ctx := context.Background()
	f.cacheLeague(yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID, yahoofixtures.PointsLeague, pgTestFetchTime)
	f.importLeague(t, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID)
	_, err := f.pool.Exec(ctx, `INSERT INTO players (id, yahoo_id, first_name, last_name) VALUES ($1, $2, 'Alex', 'Twoway')`,
		pgTestNHLPlayerID, pgTestMappedYahooID)
	require.NoError(t, err)
	// A player from an earlier snapshot that Yahoo no longer lists.
	_, err = f.pool.Exec(ctx, `INSERT INTO yahoo_league_players (league_key, season, league_id, game_key, player_id,
		player_key, full_name, eligible_positions, fetched_at) VALUES ('465.l.77777', 2026, 77777, 465, 6999,
		'465.p.6999', 'Old Timer', '{C}', $1)`, pgTestFetchTime.Add(-time.Hour))
	require.NoError(t, err)

	cachePoolSnapshot(t, f.storage, pgTestFetchTime)
	val, err := f.env.ExecuteActivity(f.acts.ImportYahooLeaguePlayers,
		ImportYahooLeaguePlayersInput{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID})
	require.NoError(t, err)
	var result ImportYahooLeaguePlayersResult
	require.NoError(t, val.Get(&result))
	assert.Equal(t, ImportYahooLeaguePlayersResult{Players: yahoofixtures.PlayersInPage, Removed: 1}, result)

	report := f.loadReport(t, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID)
	assert.Equal(t, yahoofixtures.PlayersInPage, report.Pool.Players)
	assert.Len(t, report.Pool.Unmatched, yahoofixtures.PlayersInPage-1, "only 7001 maps to an NHL player")
	require.Len(t, report.Pool.Unresolved, 1)
	assert.Equal(t, "Finn Nopos", report.Pool.Unresolved[0].Name)

	players, err := draft.LoadPool(ctx, sqlcdb.New(f.pool), "465.l.77777")
	require.NoError(t, err)
	assert.Equal(t, []string{"C", "LW", "Util"}, players[0].EligiblePositions)
	assert.EqualValues(t, pgTestNHLPlayerID, players[0].NHLPlayerID)
}

// relabelSeason turns the 2025 roto fixture into the same numeric league ID
// in a later Yahoo game, as Yahoo does when it reuses a league ID.
func relabelSeason(data []byte, season int) []byte {
	data = bytes.ReplaceAll(data, []byte("<season>2025</season>"), []byte(fmt.Sprintf("<season>%d</season>", season)))
	return bytes.ReplaceAll(data, []byte("453.l.1003"), []byte(fmt.Sprintf("%d.l.1003", pgTestLaterGameKey)))
}

// cachePoolSnapshot stores the fixture page and a manifest naming it.
func cachePoolSnapshot(t *testing.T, mem *store.MemStorage, fetched time.Time) {
	t.Helper()
	page := resource.LeaguePlayers{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID}
	mem.SetFile(page.Path(), yahoofixtures.Read(yahoofixtures.PlayersPage))
	manifest := resource.LeaguePlayerPoolManifest{
		LeagueKey: "465.l.77777", GameKey: yahoofixtures.PointsGameKey, FetchedAt: fetched,
		Starts: []int{0}, Players: yahoofixtures.PlayersInPage,
	}
	res := resource.LeaguePlayerPool{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, res, manifest))
}
