package resource_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gamePlayersDownloadID = 1758369600000

// gameEntryXML is one <game> element of a games list or a game player page;
// players, when not empty, are its <player> elements.
func gameEntryXML(code string, season, key int, players string) string {
	if players != "" {
		players = "<players>" + players + "</players>"
	}
	return fmt.Sprintf("<game><game_key>%d</game_key><code>%s</code><season>%d</season>%s</game>",
		key, code, season, players)
}

func gamesListXML(games ...string) []byte {
	return []byte("<fantasy_content><games>" + strings.Join(games, "") + "</games></fantasy_content>")
}

func gamePageXML(game string) []byte {
	return []byte("<fantasy_content>" + game + "</fantasy_content>")
}

func playerXML(key string, id int) string {
	return fmt.Sprintf("<player><player_key>%s</player_key><player_id>%d</player_id></player>", key, id)
}

func TestYahooGames_PathURLType(t *testing.T) {
	r := resource.YahooGames{}
	assert.Equal(t, "yahoo-games/games-nhl.xml", r.Path())
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/games;game_codes=nhl", r.URL())
	assert.Equal(t, core.YahooGames, r.Type())
}

func TestYahooGames_Parse(t *testing.T) {
	t.Parallel()
	content, err := resource.YahooGames{}.Parse(yahoofixtures.Read(yahoofixtures.NHLGames))
	require.NoError(t, err)
	require.Len(t, content.Games, yahoofixtures.GamesInNHLGames)

	game, ok := resource.FindNHLGame(content, yahoofixtures.CurrentGameSeason)
	require.True(t, ok)
	assert.Equal(t, yahoofixtures.CurrentGameKey, game.Key)
	assert.Zero(t, game.IsGameOver)
	past, ok := resource.FindNHLGame(content, yahoofixtures.PastGameSeason)
	require.True(t, ok)
	assert.Equal(t, yahoofixtures.PastGameKey, past.Key)
	assert.Equal(t, 1, past.IsGameOver)
	_, ok = resource.FindNHLGame(content, yahoofixtures.CurrentGameSeason+1)
	assert.False(t, ok)
}

func TestYahooGames_ParseRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"malformed_xml", []byte(invalidXML), "parse Yahoo NHL games"},
		{"no_game", gamesListXML(), "response contains no game"},
		{"other_sport", gamesListXML(gameEntryXML("nfl", 2026, 461, "")), `game key 461 is for code "nfl" season 2026`},
		{"missing_key", gamesListXML(gameEntryXML("nhl", 2026, 0, "")), `game key 0 is for code "nhl" season 2026`},
		{"missing_season", gamesListXML(gameEntryXML("nhl", 0, 477, "")), `game key 477 is for code "nhl" season 0`},
		{"season_twice", gamesListXML(gameEntryXML("nhl", 2026, 477, ""), gameEntryXML("nhl", 2026, 478, "")),
			"game key 478 or season 2026 is listed twice"},
		{"key_twice", gamesListXML(gameEntryXML("nhl", 2025, 477, ""), gameEntryXML("nhl", 2026, 477, "")),
			"game key 477 or season 2026 is listed twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			content, err := resource.YahooGames{}.Parse(tt.data)
			require.Error(t, err)
			assert.Nil(t, content)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestGamePlayers_Path(t *testing.T) {
	tests := []struct {
		name     string
		start    int
		expected string
	}{
		{"start_zero", 0, "seasons/2026/yahoo-game/players/1758369600000/players-0000.xml"},
		{"start_small", 25, "seasons/2026/yahoo-game/players/1758369600000/players-0025.xml"},
		{"start_large", 1600, "seasons/2026/yahoo-game/players/1758369600000/players-1600.xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.GamePlayers{Season: 2026, DownloadID: gamePlayersDownloadID, Start: tt.start, GameKey: 477}
			assert.Equal(t, tt.expected, r.Path())
		})
	}
}

func TestGamePlayers_URLType(t *testing.T) {
	r := resource.GamePlayers{Season: 2026, Start: 50, GameKey: 477}
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/game/477/players;start=50;count=25", r.URL())
	assert.Equal(t, core.YahooGamePlayers, r.Type())
}

func TestGamePlayers_Parse(t *testing.T) {
	t.Parallel()
	r := resource.GamePlayers{Season: yahoofixtures.CurrentGameSeason, GameKey: yahoofixtures.CurrentGameKey}

	content, err := r.Parse(yahoofixtures.Read(yahoofixtures.GamePlayersPage))

	require.NoError(t, err)
	assert.Equal(t, yahoofixtures.CurrentGameKey, content.Game.Key)
	require.Len(t, content.Game.Players.Slice, yahoofixtures.PlayersInGamePage)
	first := content.Game.Players.Slice[0]
	assert.Equal(t, "477.p.7001", first.Key)
	assert.Equal(t, 7001, first.ID)
	assert.Equal(t, "Alex Twoway", first.Name.Full)
	assert.Equal(t, "TOR", first.EditorialTeamAbbr)
}

func TestGamePlayers_ParseWithoutGameKeyChecksSeasonOnly(t *testing.T) {
	t.Parallel()
	r := resource.GamePlayers{Season: yahoofixtures.CurrentGameSeason}

	content, err := r.Parse(yahoofixtures.Read(yahoofixtures.GamePlayersPage))

	require.NoError(t, err)
	assert.Len(t, content.Game.Players.Slice, yahoofixtures.PlayersInGamePage)
}

func TestGamePlayers_ParseRejects(t *testing.T) {
	t.Parallel()
	valid := playerXML("477.p.7001", 7001)
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"malformed_xml", []byte(invalidXML), "parse game players 2026 start 0"},
		{"other_sport", gamePageXML(gameEntryXML("nfl", 2026, 477, valid)), `response game code "nfl" is not NHL`},
		{"other_season", gamePageXML(gameEntryXML("nhl", 2025, 477, valid)),
			"response season 2025 does not match requested season 2026"},
		{"no_game_key", gamePageXML(gameEntryXML("nhl", 2026, 0, valid)), "response game key 0 is not a game key"},
		{"other_game_key", gamePageXML(gameEntryXML("nhl", 2026, 478, playerXML("478.p.7001", 7001))),
			"response game key 478 does not match requested game key 477"},
		{"player_of_other_game", gamePageXML(gameEntryXML("nhl", 2026, 477, playerXML("465.p.7001", 7001))),
			`player key "465.p.7001" does not name player 7001 of game 477`},
		{"player_key_other_id", gamePageXML(gameEntryXML("nhl", 2026, 477, playerXML("477.p.7002", 7001))),
			`player key "477.p.7002" does not name player 7001 of game 477`},
		{"malformed_player_key", gamePageXML(gameEntryXML("nhl", 2026, 477, playerXML("7001", 7001))),
			`player key "7001" does not name player 7001 of game 477`},
	}
	r := resource.GamePlayers{Season: 2026, GameKey: 477}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			content, err := r.Parse(tt.data)
			require.Error(t, err)
			assert.Nil(t, content)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestGamePlayerList_PathType(t *testing.T) {
	r := resource.GamePlayerList{Season: 2026}
	assert.Equal(t, "seasons/2026/yahoo-game/players/manifest.json", r.Path())
	assert.Equal(t, core.YahooGamePlayers, r.Type())
}

func TestGamePlayerListManifest_FormatParseRoundTrip(t *testing.T) {
	t.Parallel()
	r := resource.GamePlayerList{Season: 2026}
	manifest := resource.GamePlayerListManifest{
		DownloadID: gamePlayersDownloadID,
		GameKey:    477,
		FetchedAt:  time.Date(2026, 11, 18, 12, 30, 0, 0, time.UTC),
		Starts:     []int{0, 25, 50},
		Players:    57,
	}

	data, err := r.Format(manifest)
	require.NoError(t, err)
	assert.JSONEq(t, `{"downloadId":1758369600000,"gameKey":477,"fetchedAt":"2026-11-18T12:30:00Z",`+
		`"starts":[0,25,50],"players":57}`, string(data), "the stored keys are an on-disk format")
	got, err := r.Parse(data)

	require.NoError(t, err)
	assert.Equal(t, manifest, got)
}

func TestGamePlayerListManifest_ParseError(t *testing.T) {
	t.Parallel()
	_, err := resource.GamePlayerList{Season: 2026}.Parse([]byte("not json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse game player list manifest 2026")
}
