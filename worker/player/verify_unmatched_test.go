package player

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNamesMatchForVerification_ExactMatch(t *testing.T) {
	t.Parallel()

	assert.True(t, namesMatchForVerification("Connor McDavid", "Connor McDavid"))
	assert.True(t, namesMatchForVerification("CONNOR MCDAVID", "connor mcdavid"))
}

func TestNamesMatchForVerification_AccentNormalization(t *testing.T) {
	t.Parallel()

	assert.True(t, namesMatchForVerification("Daniel Brière", "Daniel Briere"))
	assert.True(t, namesMatchForVerification("Juuso Välimäki", "Juuso Valimaki"))
}

func TestNamesMatchForVerification_CompoundFirstName(t *testing.T) {
	t.Parallel()

	// Charles Alexis Legault case: NHL has "Charles Alexis" + "Legault"
	// but search might return "Charles Alexis Legault" as full name
	assert.True(t, namesMatchForVerification("Charles Alexis Legault", "Charles Alexis Legault"))

	// Also test when comparing Yahoo's parsed version
	// Yahoo: "Charles" + "Alexis Legault" -> "Charles Alexis Legault"
	// NHL API result: "Charles Alexis Legault"
	assert.True(t, namesMatchForVerification("Charles Alexis Legault", "Charles Alexis Legault"))
}

func TestNamesMatchForVerification_PrefixMatch(t *testing.T) {
	t.Parallel()

	// Prefix matching: "alex" is a prefix of "alexander"
	assert.True(t, namesMatchForVerification("Alex Smith", "Alexander Smith"))
	assert.True(t, namesMatchForVerification("Alexander Smith", "Alex Smith"))

	// Mike/Michael - won't match because "mike" is not a prefix of "michael"
	// (the verification function is simpler than the main matcher and doesn't have nickname aliases)
	assert.False(t, namesMatchForVerification("Mike Smith", "Michael Smith"))

	// Bob/Robert - won't match because "bob" is not a prefix of "robert"
	assert.False(t, namesMatchForVerification("Bob Smith", "Robert Smith"))
}

func TestNamesMatchForVerification_DifferentLastName(t *testing.T) {
	t.Parallel()

	assert.False(t, namesMatchForVerification("Connor McDavid", "Connor Brown"))
	assert.False(t, namesMatchForVerification("Mathieu Bizier", "Mathieu Garon"))
}

func TestNamesMatchForVerification_DifferentPerson(t *testing.T) {
	t.Parallel()

	// Completely different people
	assert.False(t, namesMatchForVerification("Wayne Gretzky", "Connor McDavid"))
	assert.False(t, namesMatchForVerification("Drew Fortescue", "Drew Miller"))
}

func TestNormalizeNameForVerification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"Connor McDavid", "connor mcdavid"},
		{"UPPER CASE", "upper case"},
		{"  Spaces  ", "spaces"},
		{"Daniel Brière", "daniel briere"},
		{"Juuso Välimäki", "juuso valimaki"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeNameForVerification(tt.input))
		})
	}
}

// --- verifyUnmatchedBatchImpl tests ---

func TestVerifyUnmatchedBatchImpl_EmptyPlayers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()

	// Empty set of verified IDs
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	result, err := a.verifyUnmatchedBatchImpl(ctx, []UnmatchedYahooPlayer{})

	require.NoError(t, err)
	assert.Empty(t, result.VerifiedNonNHL)
	assert.Empty(t, result.TrulyUnmatched)
	assert.Empty(t, result.NotFoundInNHL)
	require.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestVerifyUnmatchedBatchImpl_AlreadyVerified(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()

	// Player 123 is already verified
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{"123"})

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	players := []UnmatchedYahooPlayer{
		{YahooID: 123, FirstName: "Test", LastName: "Player"},
	}

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)

	require.NoError(t, err)
	assert.Len(t, result.VerifiedNonNHL, 1)
	assert.Equal(t, store.YahooPlayerID(123), result.VerifiedNonNHL[0])
	assert.Empty(t, result.TrulyUnmatched)
	assert.Empty(t, result.NotFoundInNHL)
	// SearchPlayer should not have been called
	client.AssertNotCalled(t, "SearchPlayer")
	require.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestVerifyUnmatchedBatchImpl_PlayerNotFoundInNHL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// No pre-verified IDs
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	// Search returns empty results
	limit := maxSearchResults
	client.On("SearchPlayer", ctx, "John Doe", &limit).Return([]nhl.PlayerSearchResult{}, nil)

	// SaveVerifiedNonNHLIDs uses a pipeline - just expect the pipeline to be executed
	// We can't easily mock the pipeline, but the function logs a warning if it fails
	// The save failing doesn't affect the return value

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	players := []UnmatchedYahooPlayer{
		{YahooID: 456, FirstName: "John", LastName: "Doe"},
	}

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)

	require.NoError(t, err)
	assert.Empty(t, result.VerifiedNonNHL)
	assert.Empty(t, result.TrulyUnmatched)
	assert.Len(t, result.NotFoundInNHL, 1)
	assert.Equal(t, store.YahooPlayerID(456), result.NotFoundInNHL[0].YahooID)
	client.AssertExpectations(t)
}

func TestVerifyUnmatchedBatchImpl_PlayerFoundWithZeroGames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// No pre-verified IDs
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	// Search returns a matching player
	playerID := nhl.PlayerID(8476453)
	limit := maxSearchResults
	client.On("SearchPlayer", ctx, "Minor Leaguer", &limit).Return([]nhl.PlayerSearchResult{
		{PlayerID: playerID, Name: "Minor Leaguer"},
	}, nil)

	// Player landing file NOT in cache - need to fetch from API
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Minor"},
		LastName:  nhl.LocalizedString{Default: "Leaguer"},
		SeasonTotals: []nhl.SeasonTotal{
			{LeagueAbbrev: "AHL", GameType: nhl.GameTypeRegularSeason, GamesPlayed: 50},
		},
	}
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	players := []UnmatchedYahooPlayer{
		{YahooID: 789, FirstName: "Minor", LastName: "Leaguer"},
	}

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)

	require.NoError(t, err)
	assert.Len(t, result.VerifiedNonNHL, 1)
	assert.Equal(t, store.YahooPlayerID(789), result.VerifiedNonNHL[0])
	assert.Empty(t, result.TrulyUnmatched)
	assert.Empty(t, result.NotFoundInNHL)
	client.AssertExpectations(t)
}

func TestVerifyUnmatchedBatchImpl_PlayerFoundWithNHLGames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// No pre-verified IDs
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	// Search returns a matching player
	playerID := nhl.PlayerID(8476453)
	limit := maxSearchResults
	client.On("SearchPlayer", ctx, "Connor McDavid", &limit).Return([]nhl.PlayerSearchResult{
		{PlayerID: playerID, Name: "Connor McDavid"},
	}, nil)

	// Player landing NOT in cache - fetch from API
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		SeasonTotals: []nhl.SeasonTotal{
			{LeagueAbbrev: "NHL", GameType: nhl.GameTypeRegularSeason, GamesPlayed: 82},
			{LeagueAbbrev: "NHL", GameType: nhl.GameTypeRegularSeason, GamesPlayed: 78},
		},
	}
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	players := []UnmatchedYahooPlayer{
		{YahooID: 999, FirstName: "Connor", LastName: "McDavid"},
	}

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)

	require.NoError(t, err)
	assert.Empty(t, result.VerifiedNonNHL)
	assert.Len(t, result.TrulyUnmatched, 1)
	assert.Equal(t, store.YahooPlayerID(999), result.TrulyUnmatched[0].YahooID)
	assert.Equal(t, 160, result.TrulyUnmatched[0].NHLGames) // 82 + 78
	assert.True(t, result.TrulyUnmatched[0].HasNHLGames)
	assert.Empty(t, result.NotFoundInNHL)
	client.AssertExpectations(t)
}

func TestVerifyUnmatchedBatchImpl_RedisLoadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// Redis load fails - should continue with empty set
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetErr(errors.New("redis connection refused"))

	// Search returns empty results
	limit := maxSearchResults
	client.On("SearchPlayer", ctx, "Test Player", &limit).Return([]nhl.PlayerSearchResult{}, nil)

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	players := []UnmatchedYahooPlayer{
		{YahooID: 111, FirstName: "Test", LastName: "Player"},
	}

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)

	// Should still succeed despite Redis load error
	require.NoError(t, err)
	assert.Empty(t, result.VerifiedNonNHL)
	assert.Len(t, result.NotFoundInNHL, 1)
	client.AssertExpectations(t)
}

func TestVerifyUnmatchedBatchImpl_SearchError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// No pre-verified IDs
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	// Search returns an error
	limit := maxSearchResults
	client.On("SearchPlayer", ctx, "Error Player", &limit).Return(nil, errors.New("API error"))

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	players := []UnmatchedYahooPlayer{
		{YahooID: 222, FirstName: "Error", LastName: "Player"},
	}

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)

	// Function should still succeed
	require.NoError(t, err)
	// Player should be in NotFoundInNHL since search failed
	assert.Len(t, result.NotFoundInNHL, 1)
	client.AssertExpectations(t)
}

func TestVerifyUnmatchedBatchImpl_NameMismatchInSearch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// No pre-verified IDs
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	// Search returns results but none match the name
	playerID := nhl.PlayerID(8476453)
	limit := maxSearchResults
	client.On("SearchPlayer", ctx, "John Smith", &limit).Return([]nhl.PlayerSearchResult{
		{PlayerID: playerID, Name: "Bob Jones"}, // Different name - no match
	}, nil)

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	players := []UnmatchedYahooPlayer{
		{YahooID: 333, FirstName: "John", LastName: "Smith"},
	}

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)

	require.NoError(t, err)
	// Player goes to NotFoundInNHL because name didn't match
	assert.Empty(t, result.VerifiedNonNHL)
	assert.Empty(t, result.TrulyUnmatched)
	assert.Len(t, result.NotFoundInNHL, 1)
	assert.Equal(t, store.YahooPlayerID(333), result.NotFoundInNHL[0].YahooID)
	assert.False(t, result.NotFoundInNHL[0].FoundInNHL)
	client.AssertExpectations(t)
}

// TestVerifyUnmatchedBatchImpl_CachedLandingUsed verifies that cached landing data is used
// instead of making API calls.
//
// IMPORTANT: When pre-populating storage with serialized data, you MUST:
// 1. Include all required fields that have custom JSON marshalers (e.g., nhl.Position, nhl.Season)
// 2. Always check the error from json.Marshal - never ignore it with `_, _ :=`
// 3. Use require.NoError to fail fast if marshaling fails
//
// Unlike mock returns which pass structs directly (no serialization), storage pre-population
// goes through JSON marshal/unmarshal, so all field validation rules apply.
func TestVerifyUnmatchedBatchImpl_CachedLandingUsed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// No pre-verified IDs
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	// Search returns a matching player
	playerID := nhl.PlayerID(8476453)
	limit := maxSearchResults
	client.On("SearchPlayer", ctx, "Cached Player", &limit).Return([]nhl.PlayerSearchResult{
		{PlayerID: playerID, Name: "Cached Player"},
	}, nil)

	// Player landing IS in cache - no API call needed for landing.
	// NOTE: Position and Season are required for successful JSON round-trip.
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Cached"},
		LastName:  nhl.LocalizedString{Default: "Player"},
		Position:  nhl.PositionCenter,
		SeasonTotals: []nhl.SeasonTotal{
			{Season: nhl.NewSeason(2024), LeagueAbbrev: "AHL", GameType: nhl.GameTypeRegularSeason, GamesPlayed: 30},
		},
	}
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err, "landing must marshal successfully")
	require.NoError(t, mem.Write(context.Background(),resource.PlayerLanding{PlayerID: playerID}.Path(), landingJSON))

	a := &Activities{Storage: mem, NHLClient: client, RedisClient: redisClient}
	players := []UnmatchedYahooPlayer{
		{YahooID: 444, FirstName: "Cached", LastName: "Player"},
	}

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)

	require.NoError(t, err)
	// Player has 0 NHL games (only AHL), should be verified non-NHL
	assert.Len(t, result.VerifiedNonNHL, 1)
	assert.Equal(t, store.YahooPlayerID(444), result.VerifiedNonNHL[0])
	assert.Empty(t, result.TrulyUnmatched)
	assert.Empty(t, result.NotFoundInNHL)
	// PlayerLanding should NOT have been called (used cache)
	client.AssertNotCalled(t, "PlayerLanding")
	client.AssertExpectations(t)
}
