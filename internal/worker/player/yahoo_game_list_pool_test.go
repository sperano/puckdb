package player

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
)

// Game keys and download IDs of the test lists.
const (
	testGameKey2025    = 465
	testGameKey2026    = 477
	testDownloadID2025 = 11
	testDownloadID2026 = 22
)

// gameListPlayerXML is one <player> of a game player list page.
type gameListPlayerXML struct {
	ID       int
	First    string
	Last     string
	Team     string
	Number   int
	Position string
}

func (p gameListPlayerXML) xml(gameKey int) string {
	return fmt.Sprintf(`<player><player_key>%d.p.%d</player_key><player_id>%d</player_id>`+
		`<name><full>%s %s</full><first>%s</first><last>%s</last></name>`+
		`<editorial_team_abbr>%s</editorial_team_abbr><uniform_number>%d</uniform_number>`+
		`<display_position>%s</display_position><headshot><url>https://img/%d.png</url></headshot></player>`,
		gameKey, p.ID, p.ID, p.First, p.Last, p.First, p.Last, p.Team, p.Number, p.Position, p.ID)
}

func gamePlayersPageXML(season, gameKey int, players ...gameListPlayerXML) string {
	var b strings.Builder
	for _, p := range players {
		b.WriteString(p.xml(gameKey))
	}
	return fmt.Sprintf(`<?xml version="1.0"?><fantasy_content><game><game_key>%d</game_key><code>nhl</code>`+
		`<season>%d</season><players count="%d">%s</players></game></fantasy_content>`,
		gameKey, season, len(players), b.String())
}

func gamesListXML(games map[int]int) string {
	var b strings.Builder
	for season, key := range games {
		fmt.Fprintf(&b, `<game><game_key>%d</game_key><code>nhl</code><season>%d</season></game>`, key, season)
	}
	return `<?xml version="1.0"?><fantasy_content><games>` + b.String() + `</games></fantasy_content>`
}

type GameListPoolSuite struct {
	ActivitySuite
	mr    *miniredis.Miniredis
	mem   *store.MemStorage
	store *fakeManifestStore
	act   *Activities
}

func TestGameListPoolSuite(t *testing.T) {
	suite.Run(t, new(GameListPoolSuite))
}

func (s *GameListPoolSuite) SetupTest() {
	s.ActivitySuite.SetupTest()
	s.mr = miniredis.RunT(s.T())
	s.mem = store.NewMemStorage()
	s.store = newFakeManifestStore()
	redisClient := redis.NewClient(&redis.Options{Addr: s.mr.Addr()})
	s.T().Cleanup(func() { _ = redisClient.Close() })
	s.act = &Activities{Storage: s.mem, RedisClient: redisClient, Manifests: s.store, LeaguePlayers: fakeLeaguePlayers{}}
	s.env.RegisterActivity(s.act.BuildYahooIDPool)
	s.env.RegisterActivity(s.act.EnsureYahooIDPool)
}

func (s *GameListPoolSuite) write(path, content string) {
	s.Require().NoError(s.mem.Write(context.Background(), path, []byte(content)))
}

// commitList writes a season's pages (one per element of pages) and its
// manifest.
func (s *GameListPoolSuite) commitList(season, gameKey int, downloadID int64, pages ...[]gameListPlayerXML) {
	manifest := resource.GamePlayerListManifest{DownloadID: downloadID, GameKey: gameKey, FetchedAt: time.Unix(1, 0).UTC()}
	for i, players := range pages {
		start := i * resource.GamePlayersPageSize
		page := resource.GamePlayers{Season: season, DownloadID: downloadID, Start: start}
		s.write(page.Path(), gamePlayersPageXML(season, gameKey, players...))
		manifest.Starts = append(manifest.Starts, start)
		manifest.Players += len(players)
	}
	res := resource.GamePlayerList{Season: season}
	data, err := res.Format(manifest)
	s.Require().NoError(err)
	s.write(res.Path(), string(data))
}

// twoSeasons commits 2025 (Crosby #87 on PIT, and Old Timer, a retired
// player no later list names) and 2026
// (Crosby #87 traded to TOR in the fixture, a second page with a rookie).
func (s *GameListPoolSuite) twoSeasons() {
	s.write(resource.YahooGames{}.Path(), gamesListXML(map[int]int{2025: testGameKey2025, 2026: testGameKey2026, 2024: 453}))
	s.commitList(2025, testGameKey2025, testDownloadID2025, []gameListPlayerXML{
		{ID: 3737, First: "Sidney", Last: "Crosby", Team: "PIT", Number: 87, Position: "C"},
		{ID: 1000, First: "Old", Last: "Timer", Team: "Ari", Number: 4, Position: "D"},
	})
	s.commitList(2026, testGameKey2026, testDownloadID2026,
		[]gameListPlayerXML{{ID: 3737, First: "Sidney", Last: "Crosby", Team: "TOR", Number: 87, Position: "C,LW"}},
		[]gameListPlayerXML{{ID: 9000, First: "New", Last: "Rookie", Team: "TB", Number: 0, Position: "RW"}},
	)
}

func (s *GameListPoolSuite) build() *SaveYahooIDPoolResult {
	future, err := s.env.ExecuteActivity(s.act.BuildYahooIDPool)
	s.Require().NoError(err)
	var result *SaveYahooIDPoolResult
	s.Require().NoError(future.Get(&result))
	return result
}

func (s *GameListPoolSuite) pool() map[store.YahooPlayerID]*store.YahooPlayer {
	pool, err := LoadYahooIDPool(context.Background(), s.act.RedisClient)
	s.Require().NoError(err)
	return pool
}

func (s *GameListPoolSuite) TestBuildKeepsEachPlayerFromTheNewestSeasonListingIt() {
	s.twoSeasons()
	s.act.LeaguePlayers = fakeLeaguePlayers{rows: []sqlcdb.ListYahooLeaguePlayersForPoolRow{
		leagueRow(3737, "Sid Crosby", "PIT", "C"),
		leagueRow(7000, "Mitch Marner", "TOR", "RW"),
	}}

	result := s.build()

	s.Equal(&SaveYahooIDPoolResult{
		TotalPlayers: 4, AvailablePlayers: 4, PoolSize: 4,
		Source: YahooIDPoolSourceGameLists, ListSeasons: []int{2026, 2025}, ListEntries: 3,
	}, result)
	pool := s.pool()
	s.Equal(&store.YahooPlayer{
		YahooID: 3737, FirstName: "Sidney", LastName: "Crosby", Positions: []nhl.Position{nhl.PositionCenter, nhl.PositionLeftWing},
		JerseyNumber: 87, Team: "Toronto", ImageURL: "https://img/3737.png",
	}, pool[3737], "the 2026 list wins over 2025 and over the league row")
	s.Equal("Old", pool[1000].FirstName, "a player only an older season lists stays")
	s.Equal("Tampa Bay", pool[9000].Team, "the second page is read")
	s.Equal("Marner", pool[7000].LastName, "a league player no list names is added")
}

func (s *GameListPoolSuite) TestBuildWithoutGamesListHasOnlyLeaguePlayers() {
	s.act.LeaguePlayers = fakeLeaguePlayers{rows: []sqlcdb.ListYahooLeaguePlayersForPoolRow{leagueRow(7000, "Mitch Marner", "TOR", "RW")}}

	result := s.build()

	s.Equal(YahooIDPoolSourceGameLists, result.Source)
	s.Empty(result.ListSeasons)
	s.Equal(0, result.ListEntries)
	s.Equal(1, result.PoolSize)
}

func (s *GameListPoolSuite) TestBuildSkipsAnUnparsableManifest() {
	s.twoSeasons()
	s.write(resource.GamePlayerList{Season: 2025}.Path(), `{"downloadId":0}`)

	result := s.build()

	s.Equal([]int{2026}, result.ListSeasons)
	s.NotContains(s.pool(), store.YahooPlayerID(1000))
}

func (s *GameListPoolSuite) TestBuildFailsOnAMissingNamedPage() {
	s.twoSeasons()
	s.Require().NoError(s.mem.Delete(context.Background(),
		resource.GamePlayers{Season: 2026, DownloadID: testDownloadID2026, Start: resource.GamePlayersPageSize}.Path()))

	_, err := s.env.ExecuteActivity(s.act.BuildYahooIDPool)

	s.Require().ErrorContains(err, "players-0025.xml")
	s.False(s.mr.Exists(YahooIDPoolKey), "nothing is saved from a partial list")
}

func (s *GameListPoolSuite) TestBuildFailsOnAPageOfAnotherGame() {
	s.twoSeasons()
	page := resource.GamePlayers{Season: 2026, DownloadID: testDownloadID2026, Start: 0}
	s.write(page.Path(), gamePlayersPageXML(2026, testGameKey2025))

	_, err := s.env.ExecuteActivity(s.act.BuildYahooIDPool)

	s.Require().ErrorContains(err, "does not match requested game key")
}

func (s *GameListPoolSuite) ensure(input EnsureYahooIDPoolInput) (EnsureYahooIDPoolResult, error) {
	future, err := s.env.ExecuteActivity(s.act.EnsureYahooIDPool, input)
	if err != nil {
		return EnsureYahooIDPoolResult{}, err
	}
	var result EnsureYahooIDPoolResult
	return result, future.Get(&result)
}

func (s *GameListPoolSuite) TestLostListPoolIsRebuiltFromTheLists() {
	s.twoSeasons()
	built := s.build()
	s.mr.FlushAll()
	s.store.matched = []store.YahooPlayerID{3737}

	result, err := s.ensure(EnsureYahooIDPoolInput{
		PoolSize: built.PoolSize, Source: YahooIDPoolSourceGameLists, ListSeasons: []int{2026, 2025},
	})

	s.Require().NoError(err)
	s.Equal(EnsureYahooIDPoolResult{
		Rebuilt: true, PoolSize: 3, ListSeasons: []int{2026, 2025}, Available: 2,
	}, result)
	s.Len(s.pool(), 3)
	members, err := s.mr.Members(YahooIDAvailableKey)
	s.Require().NoError(err)
	s.ElementsMatch([]string{"1000", "9000"}, members, "matched Yahoo IDs stay out")
}

func (s *GameListPoolSuite) TestRebuildAcceptsAShorterCurrentSeasonList() {
	s.twoSeasons()
	s.commitList(2026, testGameKey2026, testDownloadID2026+1,
		[]gameListPlayerXML{{ID: 3737, First: "Sidney", Last: "Crosby", Team: "TOR", Number: 87, Position: "C"}})

	result, err := s.ensure(EnsureYahooIDPoolInput{
		PoolSize: 3, Source: YahooIDPoolSourceGameLists, ListSeasons: []int{2026, 2025},
	})

	s.Require().NoError(err)
	s.True(result.Rebuilt)
	s.Equal(2, result.PoolSize, "a refreshed list may name fewer players than phase 1's")
}

func (s *GameListPoolSuite) TestRebuildFailsWhenASeasonPhase1ReadHasNoList() {
	s.twoSeasons()
	s.Require().NoError(s.mem.Delete(context.Background(), resource.GamePlayerList{Season: 2025}.Path()))

	_, err := s.ensure(EnsureYahooIDPoolInput{
		PoolSize: 3, Source: YahooIDPoolSourceGameLists, ListSeasons: []int{2026, 2025},
	})

	s.Require().ErrorContains(err, "no committed game player list for seasons [2025]")
}

func (s *GameListPoolSuite) TestRebuildRefusesAnUnknownSource() {
	_, err := s.ensure(EnsureYahooIDPoolInput{PoolSize: 1, Source: "elsewhere"})

	s.Require().ErrorContains(err, `unknown source "elsewhere"`)
}

// TestBatchRebuildsALostListPoolFromTheLists: a batch finding a smaller
// pool than its check rebuilds it from the source the check names.
func (s *GameListPoolSuite) TestBatchRebuildsALostListPoolFromTheLists() {
	s.twoSeasons()
	input := ManifestBatchInput{
		YahooPoolSize: 3, YahooPoolSource: YahooIDPoolSourceGameLists, YahooListSeasons: []int{2026, 2025},
	}
	s.env.RegisterActivityWithOptions(func(ctx context.Context) (map[store.YahooPlayerID]*store.YahooPlayer, error) {
		return s.act.loadYahooIDPool(ctx, input.yahooPool())
	}, activity.RegisterOptions{Name: "LoadBatchPool"})

	future, err := s.env.ExecuteActivity("LoadBatchPool")
	s.Require().NoError(err)
	var pool map[store.YahooPlayerID]*store.YahooPlayer
	s.Require().NoError(future.Get(&pool))

	s.Len(pool, 3)
	s.Equal("Toronto", pool[3737].Team)
}

func TestYahooPlayerFromGameList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		player store.Player
		want   store.YahooPlayer
		ok     bool
	}{
		{
			name: "structured name, headshot, team name",
			player: store.Player{ID: 5, Name: store.PlayerName{Full: "Charles Alexis Legault", First: "Charles Alexis", Last: "Legault"},
				EditorialTeamAbbr: "MON", UniformNumber: 22, DisplayPosition: "D", Headshot: store.PlayerHeadshot{URL: "h"}, ImageURL: "i"},
			want: store.YahooPlayer{YahooID: 5, FirstName: "Charles Alexis", LastName: "Legault", Positions: []nhl.Position{nhl.PositionDefense},
				JerseyNumber: 22, Team: "Montreal", ImageURL: "h"},
			ok: true,
		},
		{
			name:   "full name only, image url fallback, unknown team",
			player: store.Player{ID: 6, Name: store.PlayerName{Full: "Pierre-Luc Dubois"}, EditorialTeamAbbr: "XXX", ImageURL: "i"},
			want:   store.YahooPlayer{YahooID: 6, FirstName: "Pierre-Luc", LastName: "Dubois", ImageURL: "i"},
			ok:     true,
		},
		{
			name:   "no name",
			player: store.Player{ID: 7, Name: store.PlayerName{Full: "  "}},
			ok:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := yahooPlayerFromGameList(tt.player)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestMissingSeasons(t *testing.T) {
	t.Parallel()
	require.Equal(t, []int{2024}, missingSeasons([]int{2026, 2024}, []int{2026, 2025}))
	require.Empty(t, missingSeasons([]int{2026}, []int{2026, 2025}))
	require.Empty(t, missingSeasons(nil, nil))
}
