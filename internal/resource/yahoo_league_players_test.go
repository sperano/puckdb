package resource_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLeaguePlayers_Path(t *testing.T) {
	tests := []struct {
		name     string
		start    int
		expected string
	}{
		{"start_zero", 0, "seasons/2026/yahoo/77777/players/1758369600000/players-0000.xml"},
		{"start_small", 25, "seasons/2026/yahoo/77777/players/1758369600000/players-0025.xml"},
		{"start_large", 1000, "seasons/2026/yahoo/77777/players/1758369600000/players-1000.xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.LeaguePlayers{Season: 2026, LeagueID: 77777, DownloadID: 1758369600000, Start: tt.start, GameKey: 465}
			assert.Equal(t, tt.expected, r.Path())
		})
	}
}

func TestLeaguePlayers_URL(t *testing.T) {
	r := resource.LeaguePlayers{Season: 2026, LeagueID: 77777, Start: 25, GameKey: 465}
	expected := "https://fantasysports.yahooapis.com/fantasy/v2/league/465.l.77777/players;start=25;count=25"
	assert.Equal(t, expected, r.URL())
	assert.Contains(t, r.URL(), "count=25", "page size must be LeaguePlayersPageSize")
}

func TestLeaguePlayers_Type(t *testing.T) {
	r := resource.LeaguePlayers{Season: 2026, LeagueID: 77777, Start: 0, GameKey: 465}
	assert.Equal(t, core.YahooLeaguePlayers, r.Type())
}

func TestLeaguePlayers_Parse(t *testing.T) {
	t.Parallel()
	r := resource.LeaguePlayers{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, Start: 0, GameKey: yahoofixtures.PointsGameKey}

	t.Run("valid_fixture", func(t *testing.T) {
		content, err := r.Parse(yahoofixtures.Read(yahoofixtures.PlayersPage))
		require.NoError(t, err)
		require.NotNil(t, content)
		assert.Len(t, content.League.Players.Slice, yahoofixtures.PlayersInPage)
		assert.Equal(t, "465.l.77777", content.League.Key)
	})

	t.Run("mismatched_league_id", func(t *testing.T) {
		mismatched := resource.LeaguePlayers{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID + 1, Start: 0, GameKey: yahoofixtures.PointsGameKey}
		_, err := mismatched.Parse(yahoofixtures.Read(yahoofixtures.PlayersPage))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match requested league")
		assert.Contains(t, err.Error(), "77777")
	})

	t.Run("mismatched_verified_game_key", func(t *testing.T) {
		mismatched := resource.LeaguePlayers{Season: yahoofixtures.PointsSeason,
			LeagueID: yahoofixtures.PointsLeagueID, Start: 0, GameKey: yahoofixtures.PointsGameKey + 1}
		_, err := mismatched.Parse(yahoofixtures.Read(yahoofixtures.PlayersPage))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match verified key")
	})

	t.Run("malformed_xml", func(t *testing.T) {
		_, err := r.Parse([]byte(invalidXML))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "77777")
	})
}

func TestLeaguePlayerPool_Path(t *testing.T) {
	r := resource.LeaguePlayerPool{Season: 2026, LeagueID: 77777}
	assert.Equal(t, "seasons/2026/yahoo/77777/players/manifest.json", r.Path())
}

func TestLeaguePlayerPool_Type(t *testing.T) {
	r := resource.LeaguePlayerPool{Season: 2026, LeagueID: 77777}
	assert.Equal(t, core.YahooLeaguePlayers, r.Type())
}

func TestLeaguePlayerPoolManifest_FormatParseRoundTrip(t *testing.T) {
	t.Parallel()
	r := resource.LeaguePlayerPool{Season: 2026, LeagueID: 77777}
	manifest := resource.LeaguePlayerPoolManifest{
		LeagueKey: "465.l.77777",
		GameKey:   465,
		FetchedAt: time.Date(2026, 9, 25, 12, 30, 0, 0, time.UTC),
		Starts:    []int{0, 25, 50},
		Players:   57,
	}

	data, err := r.Format(manifest)
	require.NoError(t, err)

	got, err := r.Parse(data)
	require.NoError(t, err)
	assert.Equal(t, manifest.LeagueKey, got.LeagueKey)
	assert.Equal(t, manifest.GameKey, got.GameKey)
	assert.True(t, manifest.FetchedAt.Equal(got.FetchedAt))
	assert.Equal(t, manifest.Starts, got.Starts)
	assert.Equal(t, manifest.Players, got.Players)
}

func TestLeaguePlayerPoolManifest_ParseError(t *testing.T) {
	t.Parallel()
	r := resource.LeaguePlayerPool{Season: 2026, LeagueID: 77777}
	_, err := r.Parse([]byte("not json"))
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "2026") && strings.Contains(err.Error(), "77777"))
}
