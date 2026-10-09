package resource_test

import (
	"encoding/xml"
	"testing"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/stretchr/testify/require"
)

const (
	gameKeyTestSeason = 2024
	gameKeyTestKey    = 453
	gameKeyTestXML    = `<game><game_key>453</game_key><code>nhl</code><season>2024</season></game>`
)

func TestGameKey_ParseShapes(t *testing.T) {
	t.Parallel()
	r := resource.GameKey{Season: gameKeyTestSeason}
	for _, tc := range []struct {
		name       string
		xmlData    string
		collection bool
	}{
		{"direct game", `<fantasy_content>` + gameKeyTestXML + `</fantasy_content>`, false},
		{"games collection", `<fantasy_content><games>` + gameKeyTestXML + `</games></fantasy_content>`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := r.Parse([]byte(tc.xmlData))
			require.NoError(t, err)
			require.NotNil(t, content)
			game := content.Game
			if tc.collection {
				require.Len(t, content.Games, 1)
				game = content.Games[0]
			}
			require.Equal(t, gameKeyTestKey, game.Key)
			require.Equal(t, gameKeyTestSeason, game.Season)
			require.Equal(t, "nhl", game.Code)

			key, err := r.ParseKey([]byte(tc.xmlData))
			require.NoError(t, err)
			require.Equal(t, gameKeyTestKey, key)
		})
	}
}

func TestGameKey_RejectInvalidGame(t *testing.T) {
	t.Parallel()
	r := resource.GameKey{Season: gameKeyTestSeason}
	for name, xmlData := range map[string]string{
		"missing_game": minimalFantasyXML,
		"zero_key":     `<fantasy_content><games><game><game_key>0</game_key><code>nhl</code><season>2024</season></game></games></fantasy_content>`,
		"wrong_sport":  `<fantasy_content><games><game><game_key>453</game_key><code>nfl</code><season>2024</season></game></games></fantasy_content>`,
		"wrong_season": `<fantasy_content><games><game><game_key>453</game_key><code>nhl</code><season>2023</season></game></games></fantasy_content>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := r.Parse([]byte(xmlData))
			require.Error(t, err)
			_, err = r.ParseKey([]byte(xmlData))
			require.Error(t, err)
		})
	}
}

func TestGameKey_RejectMalformedXML(t *testing.T) {
	t.Parallel()
	r := resource.GameKey{Season: gameKeyTestSeason}
	data := []byte(`<fantasy_content><games>`)

	_, err := r.Parse(data)
	var syntaxError *xml.SyntaxError
	require.ErrorAs(t, err, &syntaxError)
	require.ErrorContains(t, err, "2024")

	_, err = r.ParseKey(data)
	require.ErrorAs(t, err, &syntaxError)
	require.ErrorContains(t, err, "2024")
}
