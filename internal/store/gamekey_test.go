package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseXML(t *testing.T) {
	t.Parallel()

	t.Run("valid game response", func(t *testing.T) {
		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <game>
    <game_id>419</game_id>
    <game_key>419</game_key>
    <name>Hockey</name>
    <season>2024</season>
  </game>
</fantasy_content>`)

		result, err := ParseXML(xmlData)
		require.NoError(t, err)
		assert.Equal(t, 419, result.Game.Key)
		assert.Equal(t, 2024, result.Game.Season)
	})

	t.Run("games array response", func(t *testing.T) {
		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <games>
    <game>
      <game_id>419</game_id>
      <game_key>419</game_key>
      <name>Hockey</name>
      <season>2024</season>
    </game>
  </games>
</fantasy_content>`)

		result, err := ParseXML(xmlData)
		require.NoError(t, err)
		assert.Len(t, result.Games, 1)
		assert.Equal(t, 419, result.Games[0].Key)
	})

	t.Run("invalid XML", func(t *testing.T) {
		xmlData := []byte(`not valid xml`)

		_, err := ParseXML(xmlData)
		require.Error(t, err)
	})
}
