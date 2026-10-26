package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSeasons(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("testdata/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 2, len(seasons))

	// Check 2021 season
	season2021, ok := seasons[2021]
	assert.True(t, ok)
	assert.Equal(t, 1, len(season2021.Leagues))
	assert.Equal(t, 22030, season2021.Leagues[0].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 6, 7, 8, 9, 10, 11}, season2021.Leagues[0].TeamIDs)

	// Check 2022 season
	season2022, ok := seasons[2022]
	assert.True(t, ok)
	assert.Equal(t, 2, len(season2022.Leagues))
	assert.Equal(t, 1003, season2022.Leagues[0].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, season2022.Leagues[0].TeamIDs)
	assert.Equal(t, 12345, season2022.Leagues[1].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, season2022.Leagues[1].TeamIDs)
}

func TestGetSeasons_FileNotFound(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("foo")
	assert.Nil(t, seasons)
	assert.True(t, strings.HasPrefix(err.Error(), "can't read yahoo seasons config file foo: "))
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestGetSeasons_UnmarshallErr(t *testing.T) {
	t.Parallel()
	// Any existing non-YAML file will do; this test's own source is always
	// present and independent of where the package sits in the tree.
	const notYAML = "seasons_test.go"
	seasons, err := getYahooSeasons(notYAML)
	assert.Nil(t, seasons)
	assert.True(t, strings.HasPrefix(err.Error(), "can't unmarshal yahoo seasons config file "+notYAML+": "))
}

func TestSeasonsMapAccess(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("testdata/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Test direct map access
	season, ok := seasons[2021]
	assert.True(t, ok)
	assert.Equal(t, 1, len(season.Leagues))

	// Test non-existent season
	_, ok = seasons[9999]
	assert.False(t, ok)
}

func TestLeagueGet(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("testdata/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	season, ok := seasons[2021]
	if !ok {
		t.Fatal("season 2021 not found")
	}
	league, err := season.GetLeague(22030)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 22030, league.LeagueID)
}

func TestLeagueGet_Error(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("testdata/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	season, ok := seasons[2021]
	if !ok {
		t.Fatal("season 2021 not found")
	}
	_, err = season.GetLeague(9999)
	assert.Equal(t, "league not found: 9999", err.Error())
}

func TestYahooSeasonsCache_LoadsEachPath(t *testing.T) {
	t.Parallel()
	var cache yahooSeasonsCache

	first, err := cache.load("testdata/seasons.yaml")
	require.NoError(t, err)
	assert.Contains(t, first, 2021)

	second, err := cache.load("testdata/seasons_temporary_metadata.yaml")
	require.NoError(t, err)
	assert.Contains(t, second, 2026, "a new path must be loaded, not answered from the previous one")
	assert.NotContains(t, second, 2021)
}

func TestYahooSeasonsCache_ReusesLoadedPath(t *testing.T) {
	t.Parallel()
	path := writeSeasonsFile(t, minimalSeasonsYAML)
	var cache yahooSeasonsCache
	first, err := cache.load(path)
	require.NoError(t, err)

	require.NoError(t, os.Remove(path))
	second, err := cache.load(path)

	require.NoError(t, err, "a loaded path must not be read again")
	assert.Equal(t, first, second)
}

func TestYahooSeasonsCache_RetriesFailedLoad(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "seasons.yaml")
	var cache yahooSeasonsCache
	_, err := cache.load(path)
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, os.WriteFile(path, []byte(minimalSeasonsYAML), seasonsFileMode))
	seasons, err := cache.load(path)

	require.NoError(t, err, "a failed load must not be kept")
	assert.Equal(t, 1, seasons[2021].Leagues[0].LeagueID)
}

func TestYahooSeasonsCache_EmptyPathIsNotConfigured(t *testing.T) {
	t.Parallel()
	var cache yahooSeasonsCache
	_, err := cache.load("")
	assert.ErrorIs(t, err, ErrYahooNotConfigured)
}

func TestGetYahooSeasonsConfig_FollowsConfiguredPath(t *testing.T) {
	// Not parallel - modifies global viper state
	t.Cleanup(viper.Reset)

	viper.Set(FlagYahooSeasons, "")
	_, err := GetYahooSeasonsConfig()
	require.ErrorIs(t, err, ErrYahooNotConfigured)

	viper.Set(FlagYahooSeasons, "testdata/seasons.yaml")
	seasons, err := GetYahooSeasonsConfig()
	require.NoError(t, err)
	assert.Contains(t, seasons, 2021)

	viper.Set(FlagYahooSeasons, "testdata/seasons_temporary_metadata.yaml")
	seasons, err = GetYahooSeasonsConfig()
	require.NoError(t, err)
	assert.Contains(t, seasons, 2026)
}

const (
	seasonsFileMode    = 0o600
	minimalSeasonsYAML = "2021:\n  leagues:\n    - league_id: 1\n"
)

func writeSeasonsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seasons.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), seasonsFileMode))
	return path
}

func TestGetSeasons_TemporaryMetadataFrom(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("testdata/seasons_temporary_metadata.yaml")
	require.NoError(t, err)

	standIn, plain := seasons[2026].Leagues[0], seasons[2026].Leagues[1]
	assert.True(t, standIn.UsesTemporaryMetadata())
	assert.Equal(t, &LeagueMetadataSource{Season: 2025, LeagueID: 1003}, standIn.TemporaryMetadataFrom)
	assert.False(t, plain.UsesTemporaryMetadata())
	assert.False(t, seasons[2025].Leagues[0].UsesTemporaryMetadata())
}

func TestGetSeasons_TemporaryMetadataFromMustBeEarlierSeason(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("testdata/seasons_temporary_metadata_invalid.yaml")
	assert.Nil(t, seasons)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "season 2026 league 1001: temporary_metadata_from must name an earlier season")
}

func TestValidateTemporaryMetadata_RequiresLeagueID(t *testing.T) {
	t.Parallel()
	seasons := YahooSeasonsMap{2026: {Leagues: []League{
		{LeagueID: 1001, TemporaryMetadataFrom: &LeagueMetadataSource{Season: 2025}},
	}}}
	assert.Error(t, validateTemporaryMetadata(seasons))
}

// Leagues are recorded in Temporal history; a league without a stand-in must
// keep serializing exactly as it did before the field existed.
func TestLeagueJSON_OmitsAbsentTemporaryMetadata(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(League{LeagueID: 1001, TeamIDs: []int{1}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"leagueId":1001,"teamIds":[1]}`, string(data))
}
