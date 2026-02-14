package store

import (
	"errors"
	"os"
	"testing"

	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewStore(t *testing.T) {
	// Set up viper with a test path
	viper.Set(config.FlagDataPath, "/tmp/test-data")
	defer viper.Reset()

	store := NewStore()
	assert.NotNil(t, store)

	// Should return an InstrumentedStore
	_, ok := store.(*InstrumentedStore)
	assert.True(t, ok)

	// Verify the store uses the configured path by checking FullPath
	testFile := GameKeyFile{Season: 2024}
	fullPath := store.FullPath(testFile)
	assert.Equal(t, "/tmp/test-data/game-keys/gamekey-2024.xml", fullPath)
}

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

	t.Run("file exists - read from store", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <game>
    <game_key>419</game_key>
    <season>2024</season>
  </game>
</fantasy_content>`)

		mockStore.On("Exists", file).Return(true)
		mockStore.On("Read", file).Return(xmlData, nil)

		gameKey, err := GetGameKey(mockStore, 2024, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 419, gameKey)
		mockStore.AssertExpectations(t)
	})

	t.Run("file exists - games array response", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <games>
    <game>
      <game_key>418</game_key>
      <season>2024</season>
    </game>
  </games>
</fantasy_content>`)

		mockStore.On("Exists", file).Return(true)
		mockStore.On("Read", file).Return(xmlData, nil)

		gameKey, err := GetGameKey(mockStore, 2024, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 418, gameKey)
		mockStore.AssertExpectations(t)
	})

	t.Run("file exists - read error", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		mockStore.On("Exists", file).Return(true)
		mockStore.On("Read", file).Return(nil, errors.New("read error"))

		_, err := GetGameKey(mockStore, 2024, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read stored game key")
		mockStore.AssertExpectations(t)
	})

	t.Run("file not exists - fetch and store", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <game>
    <game_key>419</game_key>
    <season>2024</season>
  </game>
</fantasy_content>`)

		mockStore.On("Exists", file).Return(false)
		mockStore.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
		mockStore.On("Write", file, xmlData).Return(nil)

		fetcher := func(url string) ([]byte, error) {
			assert.Contains(t, url, "season=2024")
			return xmlData, nil
		}

		postDownloadCalled := false
		postDownload := func() {
			postDownloadCalled = true
		}

		gameKey, err := GetGameKey(mockStore, 2024, fetcher, postDownload)
		require.NoError(t, err)
		assert.Equal(t, 419, gameKey)
		assert.True(t, postDownloadCalled)
		mockStore.AssertExpectations(t)
	})

	t.Run("file not exists - fetch error", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		mockStore.On("Exists", file).Return(false)

		fetcher := func(url string) ([]byte, error) {
			return nil, errors.New("network error")
		}

		_, err := GetGameKey(mockStore, 2024, fetcher, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch game key")
		mockStore.AssertExpectations(t)
	})

	t.Run("file not exists - mkdir error", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		xmlData := []byte(`<fantasy_content><game><game_key>419</game_key></game></fantasy_content>`)

		mockStore.On("Exists", file).Return(false)
		mockStore.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(errors.New("mkdir error"))

		fetcher := func(url string) ([]byte, error) {
			return xmlData, nil
		}

		_, err := GetGameKey(mockStore, 2024, fetcher, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mkdir for game key")
		mockStore.AssertExpectations(t)
	})

	t.Run("file not exists - write error", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		xmlData := []byte(`<fantasy_content><game><game_key>419</game_key></game></fantasy_content>`)

		mockStore.On("Exists", file).Return(false)
		mockStore.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
		mockStore.On("Write", file, mock.Anything).Return(errors.New("write error"))

		fetcher := func(url string) ([]byte, error) {
			return xmlData, nil
		}

		_, err := GetGameKey(mockStore, 2024, fetcher, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "save game key")
		mockStore.AssertExpectations(t)
	})

	t.Run("invalid XML response", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		mockStore.On("Exists", file).Return(true)
		mockStore.On("Read", file).Return([]byte("not valid xml"), nil)

		_, err := GetGameKey(mockStore, 2024, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse game key response")
		mockStore.AssertExpectations(t)
	})

	t.Run("no game key in response", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		// Empty response with no game key
		xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
</fantasy_content>`)

		mockStore.On("Exists", file).Return(true)
		mockStore.On("Read", file).Return(xmlData, nil)

		_, err := GetGameKey(mockStore, 2024, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no game key found")
		mockStore.AssertExpectations(t)
	})

	t.Run("nil postDownload callback", func(t *testing.T) {
		mockStore := &MockStore{}
		file := GameKeyFile{Season: 2024}

		xmlData := []byte(`<fantasy_content><game><game_key>419</game_key></game></fantasy_content>`)

		mockStore.On("Exists", file).Return(false)
		mockStore.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
		mockStore.On("Write", file, xmlData).Return(nil)

		fetcher := func(url string) ([]byte, error) {
			return xmlData, nil
		}

		// nil postDownload should not panic
		gameKey, err := GetGameKey(mockStore, 2024, fetcher, nil)
		require.NoError(t, err)
		assert.Equal(t, 419, gameKey)
		mockStore.AssertExpectations(t)
	})
}
