package store

import (
	"errors"
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

func TestGetGameKey(t *testing.T) {
	t.Parallel()

	t.Run("file exists - read from repos", func(t *testing.T) {
		mem := NewMemStorage()
		repos := NewRepos(mem)

		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <game>
    <game_key>419</game_key>
    <season>2024</season>
  </game>
</fantasy_content>`)

		// Pre-populate the game key file
		err := repos.Yahoo.SaveGameKey(2024, xmlData)
		require.NoError(t, err)

		gameKey, err := GetGameKey(repos, 2024, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 419, gameKey)
	})

	t.Run("file exists - games array response", func(t *testing.T) {
		mem := NewMemStorage()
		repos := NewRepos(mem)

		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <games>
    <game>
      <game_key>418</game_key>
      <season>2024</season>
    </game>
  </games>
</fantasy_content>`)

		err := repos.Yahoo.SaveGameKey(2024, xmlData)
		require.NoError(t, err)

		gameKey, err := GetGameKey(repos, 2024, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 418, gameKey)
	})

	t.Run("file exists - parse error", func(t *testing.T) {
		mem := NewMemStorage()
		repos := NewRepos(mem)

		// Store invalid data
		err := repos.Yahoo.SaveGameKey(2024, []byte("not valid xml"))
		require.NoError(t, err)

		_, err = GetGameKey(repos, 2024, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read stored game key")
	})

	t.Run("file not exists - fetch and store", func(t *testing.T) {
		mem := NewMemStorage()
		repos := NewRepos(mem)

		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <game>
    <game_key>419</game_key>
    <season>2024</season>
  </game>
</fantasy_content>`)

		fetcher := func(url string) ([]byte, error) {
			assert.Contains(t, url, "seasons=2024")
			return xmlData, nil
		}

		postDownloadCalled := false
		postDownload := func() {
			postDownloadCalled = true
		}

		gameKey, err := GetGameKey(repos, 2024, fetcher, postDownload)
		require.NoError(t, err)
		assert.Equal(t, 419, gameKey)
		assert.True(t, postDownloadCalled)

		// Verify the file was stored
		assert.True(t, repos.Yahoo.GameKeyExists(2024))
	})

	t.Run("file not exists - fetch error", func(t *testing.T) {
		mem := NewMemStorage()
		repos := NewRepos(mem)

		fetcher := func(url string) ([]byte, error) {
			return nil, errors.New("network error")
		}

		_, err := GetGameKey(repos, 2024, fetcher, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch game key")
	})

	t.Run("invalid XML response from fetcher", func(t *testing.T) {
		mem := NewMemStorage()
		repos := NewRepos(mem)

		fetcher := func(url string) ([]byte, error) {
			return []byte("not valid xml"), nil
		}

		_, err := GetGameKey(repos, 2024, fetcher, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse game key response")
	})

	t.Run("no game key in response", func(t *testing.T) {
		mem := NewMemStorage()
		repos := NewRepos(mem)

		// Empty response with no game key
		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
</fantasy_content>`)

		err := repos.Yahoo.SaveGameKey(2024, xmlData)
		require.NoError(t, err)

		_, err = GetGameKey(repos, 2024, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no game key found")
	})

	t.Run("nil postDownload callback", func(t *testing.T) {
		mem := NewMemStorage()
		repos := NewRepos(mem)

		xmlData := []byte(`<fantasy_content><game><game_key>419</game_key></game></fantasy_content>`)

		fetcher := func(url string) ([]byte, error) {
			return xmlData, nil
		}

		// nil postDownload should not panic
		gameKey, err := GetGameKey(repos, 2024, fetcher, nil)
		require.NoError(t, err)
		assert.Equal(t, 419, gameKey)
	})
}

func TestExtractGameKey(t *testing.T) {
	t.Parallel()

	t.Run("from game field", func(t *testing.T) {
		fantasy := &FantasyContent{
			Game: FantasyGame{Key: 419},
		}
		key, err := extractGameKey(fantasy, 2024)
		require.NoError(t, err)
		assert.Equal(t, 419, key)
	})

	t.Run("from games array", func(t *testing.T) {
		fantasy := &FantasyContent{
			Games: []FantasyGame{{Key: 418}},
		}
		key, err := extractGameKey(fantasy, 2024)
		require.NoError(t, err)
		assert.Equal(t, 418, key)
	})

	t.Run("games array takes precedence", func(t *testing.T) {
		fantasy := &FantasyContent{
			Game:  FantasyGame{Key: 419},
			Games: []FantasyGame{{Key: 418}},
		}
		key, err := extractGameKey(fantasy, 2024)
		require.NoError(t, err)
		assert.Equal(t, 418, key)
	})

	t.Run("no game key", func(t *testing.T) {
		fantasy := &FantasyContent{}
		_, err := extractGameKey(fantasy, 2024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no game key found for season 2024")
	})
}
