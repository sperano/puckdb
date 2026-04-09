package yahoo

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- extractGameKey ---

func TestExtractGameKey_FromGamesSlice(t *testing.T) {
	t.Parallel()

	fantasy := &store.FantasyContent{
		Games: []store.FantasyGame{{Key: 423}},
	}

	key, err := extractGameKey(fantasy, 2023)

	require.NoError(t, err)
	assert.Equal(t, 423, key)
}

func TestExtractGameKey_FromGameField(t *testing.T) {
	t.Parallel()

	fantasy := &store.FantasyContent{
		Game: store.FantasyGame{Key: 419},
	}

	key, err := extractGameKey(fantasy, 2019)

	require.NoError(t, err)
	assert.Equal(t, 419, key)
}

func TestExtractGameKey_GamesSliceTakesPrecedence(t *testing.T) {
	t.Parallel()

	// When both are present, Games[0] wins.
	fantasy := &store.FantasyContent{
		Games: []store.FantasyGame{{Key: 423}},
		Game:  store.FantasyGame{Key: 999},
	}

	key, err := extractGameKey(fantasy, 2023)

	require.NoError(t, err)
	assert.Equal(t, 423, key)
}

func TestExtractGameKey_NoGameFound(t *testing.T) {
	t.Parallel()

	fantasy := &store.FantasyContent{}

	_, err := extractGameKey(fantasy, 2023)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no game key found")
	assert.Contains(t, err.Error(), "2023")
}

// --- getGameKeyImpl ---

func TestGetGameKeyImpl_FromCache(t *testing.T) {
	t.Parallel()

	// Write a valid game key XML into mem storage so getGameKeyImpl reads from cache.
	mem := store.NewMemStorage()
	gobCache := cache.NewGobCache(nil) // nil client: gob cache is only populated after a parse
	season := 2023

	xmlContent := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><games><game><game_key>423</game_key><game_id>423</game_id></game></games></fantasy_content>`)
	res := resource.GameKey{Season: season}
	require.NoError(t, mem.Write(res.Path(), xmlContent))

	key, err := getGameKeyImpl(context.Background(), mem, gobCache, season, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, 423, key)
}

func TestGetGameKeyImpl_FetcherCalled_WhenNoCache(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	gobCache := cache.NewGobCache(nil)
	season := 2023

	xmlContent := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><games><game><game_key>423</game_key><game_id>423</game_id></game></games></fantasy_content>`)

	fetcher := mockDownloader(xmlContent, nil)
	postDownload := func() {} // no-op

	key, err := getGameKeyImpl(context.Background(), mem, gobCache, season, fetcher, postDownload)

	require.NoError(t, err)
	assert.Equal(t, 423, key)
}

func TestGetGameKeyImpl_FetcherError(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	gobCache := cache.NewGobCache(nil)

	fetcher := mockDownloader(nil, assert.AnError)

	_, err := getGameKeyImpl(context.Background(), mem, gobCache, 2023, fetcher, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch game key")
}

// --- collectSummaryParams ---

func TestCollectSummaryParams_NoFiles(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	teams := []TeamInfo{{LeagueID: 1, TeamID: 1}}

	summaryParams := a.collectSummaryParams(
		context.Background(),
		teams,
		time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		make(core.OriginCounts),
	)

	assert.Empty(t, summaryParams)
}

func TestCollectSummaryParams_ValidFile(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	teams := []TeamInfo{{LeagueID: 12345, TeamID: 1}}

	// Write a minimal team summary XML file.
	xmlContent := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <team>
    <team_stats>
      <coverage_type>date</coverage_type>
      <stats>
        <stat><stat_id>1</stat_id><value>5</value></stat>
        <stat><stat_id>2</stat_id><value>10</value></stat>
      </stats>
    </team_stats>
  </team>
</fantasy_content>`)
	res := resource.TeamSummary{LeagueID: 12345, TeamID: 1, Date: date}
	require.NoError(t, mem.Write(res.Path(), xmlContent))

	summaryParams := a.collectSummaryParams(
		context.Background(),
		teams,
		date,
		make(core.OriginCounts),
	)

	require.Len(t, summaryParams, 1)
	assert.Equal(t, int32(12345), summaryParams[0].LeagueID)
	assert.Equal(t, int32(1), summaryParams[0].TeamID)
	assert.Equal(t, "date", summaryParams[0].CoverageType)
	assert.Equal(t, float32(5), summaryParams[0].Goals.Float32)
	assert.Equal(t, float32(10), summaryParams[0].Assists.Float32)
}

func TestCollectSummaryParams_MultipleTeams(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	teams := []TeamInfo{
		{LeagueID: 12345, TeamID: 1},
		{LeagueID: 12345, TeamID: 2},
		{LeagueID: 12345, TeamID: 3}, // this one has no file
	}

	xmlContent := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><team><team_stats><coverage_type>date</coverage_type><stats></stats></team_stats></team></fantasy_content>`)
	for _, id := range []int{1, 2} {
		res := resource.TeamSummary{LeagueID: 12345, TeamID: id, Date: date}
		require.NoError(t, mem.Write(res.Path(), xmlContent))
	}

	summaryParams := a.collectSummaryParams(
		context.Background(),
		teams,
		date,
		make(core.OriginCounts),
	)

	// Only the two teams with files produce summaries.
	assert.Len(t, summaryParams, 2)
}

// --- collectRosterParams ---

func TestCollectRosterParams_NoFiles(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	teams := []TeamInfo{{LeagueID: 1, TeamID: 1}}

	params := a.collectRosterParams(
		context.Background(),
		teams,
		time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		make(core.OriginCounts),
	)

	assert.Empty(t, params)
}

func TestCollectRosterParams_ValidFile(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	teams := []TeamInfo{{LeagueID: 12345, TeamID: 1}}

	xmlContent := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <team>
    <roster>
      <coverage_type>date</coverage_type>
      <is_editable>1</is_editable>
      <players count="2">
        <player>
          <player_id>6616</player_id>
          <player_key>423.p.6616</player_key>
          <selected_position>
            <coverage_type>date</coverage_type>
            <position>C</position>
            <is_flex>0</is_flex>
          </selected_position>
        </player>
        <player>
          <player_id>5441</player_id>
          <player_key>423.p.5441</player_key>
          <selected_position>
            <coverage_type>date</coverage_type>
            <position>LW</position>
            <is_flex>0</is_flex>
          </selected_position>
        </player>
      </players>
    </roster>
  </team>
</fantasy_content>`)
	res := resource.Roster{LeagueID: 12345, TeamID: 1, Date: date}
	require.NoError(t, mem.Write(res.Path(), xmlContent))

	params := a.collectRosterParams(
		context.Background(),
		teams,
		date,
		make(core.OriginCounts),
	)

	require.Len(t, params, 2)
	assert.Equal(t, int32(12345), params[0].LeagueID)
	assert.Equal(t, int32(1), params[0].TeamID)
	assert.Equal(t, int32(6616), params[0].PlayerID)
	assert.Equal(t, "C", params[0].SelectedPosition)
	assert.Equal(t, int32(5441), params[1].PlayerID)
	assert.Equal(t, "LW", params[1].SelectedPosition)
}
