package worker

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	// verifyAPIDelay is the delay between NHL API calls during verification.
	verifyAPIDelay = 200 * time.Millisecond

	// maxSearchResults is the maximum number of search results to consider.
	maxSearchResults = 20

	// leagueNHL is the league abbreviation for NHL.
	leagueNHL = "NHL"

	// DefaultVerifyBatchSize is the number of players to verify per batch activity.
	DefaultVerifyBatchSize = 10
)

// VerifiedPlayer contains the verification result for a Yahoo player.
type VerifiedPlayer struct {
	YahooID     store.YahooPlayerID
	FirstName   string
	LastName    string
	NHLGames    int  // Total NHL regular season games
	HasNHLGames bool // True if player has any NHL games
	FoundInNHL  bool // True if player was found in NHL search
	NHLPlayerID int64
	NHLName     string // The matched NHL player name (may differ from Yahoo name)
}

// VerifyUnmatchedResult contains the categorized results of verification.
type VerifyUnmatchedResult struct {
	// VerifiedNonNHL contains Yahoo IDs confirmed to have 0 NHL games.
	VerifiedNonNHL []store.YahooPlayerID

	// TrulyUnmatched contains players who have NHL games but weren't matched.
	// These need investigation.
	TrulyUnmatched []VerifiedPlayer

	// NotFoundInNHL contains players not found in the NHL search at all.
	NotFoundInNHL []VerifiedPlayer
}

// verifyDeps contains the dependencies for verifying unmatched players.
type verifyDeps struct {
	client      NHLClient
	fs          store.Store
	redisClient cache.Client
}

// VerifyUnmatchedBatchActivity verifies a batch of unmatched Yahoo players against the NHL API.
// This is called multiple times by the workflow to enable progress tracking.
// It categorizes players into:
// - VerifiedNonNHL: confirmed 0 NHL games (can be ignored in future)
// - TrulyUnmatched: have NHL games but weren't matched (need investigation)
// - NotFoundInNHL: no matching name in NHL database
func VerifyUnmatchedBatchActivity(ctx context.Context, players []UnmatchedYahooPlayer) (*VerifyUnmatchedResult, error) {
	logger := activity.GetLogger(ctx)
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	deps := verifyDeps{
		client:      nhl.NewClient(),
		fs:          store.NewStore(),
		redisClient: redisClient,
	}

	result, err := verifyUnmatchedBatchImpl(ctx, deps, players)
	if err != nil {
		return nil, err
	}

	logger.Debug("Verified batch",
		"batch_size", len(players),
		"verified_non_nhl", len(result.VerifiedNonNHL),
		"truly_unmatched", len(result.TrulyUnmatched),
		"not_found", len(result.NotFoundInNHL))

	return result, nil
}

func verifyUnmatchedBatchImpl(ctx context.Context, deps verifyDeps, players []UnmatchedYahooPlayer) (*VerifyUnmatchedResult, error) {
	// Load already verified IDs to skip re-verification
	alreadyVerified, err := LoadVerifiedNonNHLIDs(ctx, deps.redisClient)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load verified non-NHL IDs, will verify all")
		alreadyVerified = make(map[store.YahooPlayerID]struct{})
	}

	result := &VerifyUnmatchedResult{
		VerifiedNonNHL: make([]store.YahooPlayerID, 0),
		TrulyUnmatched: make([]VerifiedPlayer, 0),
		NotFoundInNHL:  make([]VerifiedPlayer, 0),
	}

	// Track newly verified non-NHL IDs
	newlyVerifiedNonNHL := make([]store.YahooPlayerID, 0)

	for i, player := range players {
		// Skip already verified players
		if _, ok := alreadyVerified[player.YahooID]; ok {
			result.VerifiedNonNHL = append(result.VerifiedNonNHL, player.YahooID)
			continue
		}

		// Verify this player
		verified := verifyPlayer(ctx, deps.client, deps.fs, player)

		if !verified.FoundInNHL {
			result.NotFoundInNHL = append(result.NotFoundInNHL, verified)
			// Not found = treat as non-NHL (they don't exist in NHL database)
			newlyVerifiedNonNHL = append(newlyVerifiedNonNHL, player.YahooID)
		} else if verified.HasNHLGames {
			result.TrulyUnmatched = append(result.TrulyUnmatched, verified)
		} else {
			result.VerifiedNonNHL = append(result.VerifiedNonNHL, player.YahooID)
			newlyVerifiedNonNHL = append(newlyVerifiedNonNHL, player.YahooID)
		}

		// Rate limiting between players (not after last one)
		if i < len(players)-1 {
			time.Sleep(verifyAPIDelay)
		}
	}

	// Save newly verified non-NHL IDs to Redis
	if len(newlyVerifiedNonNHL) > 0 {
		if err := SaveVerifiedNonNHLIDs(ctx, deps.redisClient, newlyVerifiedNonNHL); err != nil {
			log.Warn().Err(err).Msg("Failed to save verified non-NHL IDs to Redis")
		}
	}

	return result, nil
}

// verifyPlayer checks if a Yahoo player has any NHL regular season games.
func verifyPlayer(ctx context.Context, client NHLClient, fs store.Store, player UnmatchedYahooPlayer) VerifiedPlayer {
	result := VerifiedPlayer{
		YahooID:   player.YahooID,
		FirstName: player.FirstName,
		LastName:  player.LastName,
	}

	playerName := player.FirstName + " " + player.LastName

	// Search for the player
	limit := maxSearchResults
	searchResults, err := client.SearchPlayer(ctx, playerName, &limit)
	if err != nil {
		log.Warn().Err(err).Str("name", playerName).Msg("Search failed")
		return result
	}

	if len(searchResults) == 0 {
		return result
	}

	// Find a matching name in the search results
	matchedResult := findMatchingPlayer(playerName, searchResults)
	if matchedResult == nil {
		return result
	}

	result.FoundInNHL = true
	result.NHLPlayerID = matchedResult.PlayerID.AsInt64()
	result.NHLName = matchedResult.Name

	// Get full player landing data - check cache first
	landing, err := fetchPlayerLanding(ctx, client, fs, matchedResult.PlayerID)
	if err != nil {
		log.Warn().Err(err).Str("name", playerName).Msg("Failed to get landing data")
		return result
	}

	// Calculate total NHL regular season games
	nhlGames := 0
	if landing.SeasonTotals != nil {
		for _, season := range landing.SeasonTotals {
			if season.LeagueAbbrev == leagueNHL && season.GameType == nhl.GameTypeRegularSeason {
				nhlGames += season.GamesPlayed
			}
		}
	}

	result.NHLGames = nhlGames
	result.HasNHLGames = nhlGames > 0

	return result
}

// fetchPlayerLanding retrieves player landing data, using cache if available.
func fetchPlayerLanding(ctx context.Context, client NHLClient, fs store.Store, playerID nhl.PlayerID) (*nhl.PlayerLanding, error) {
	file := store.PlayerLandingFile{PlayerID: playerID}

	// Check cache first
	if fs.Exists(file) {
		content, err := fs.Read(file)
		if err == nil {
			var landing nhl.PlayerLanding
			if err := json.Unmarshal(content, &landing); err == nil {
				return &landing, nil
			}
		}
	}

	// Not in cache, fetch from API
	return client.PlayerLanding(ctx, playerID)
}

// findMatchingPlayer finds a search result where the name actually matches.
func findMatchingPlayer(searchedName string, results []nhl.PlayerSearchResult) *nhl.PlayerSearchResult {
	for i := range results {
		if namesMatchForVerification(searchedName, results[i].Name) {
			return &results[i]
		}
	}
	return nil
}

// namesMatchForVerification checks if two player names refer to the same person.
// Uses the same logic as the verify_unmatched command.
func namesMatchForVerification(searchedName, resultName string) bool {
	searchedNorm := normalizeNameForVerification(searchedName)
	resultNorm := normalizeNameForVerification(resultName)

	// Full name comparison (handles compound names like "Charles Alexis Legault")
	if searchedNorm == resultNorm {
		return true
	}

	// Extract first and last names
	searchedParts := strings.Fields(searchedNorm)
	resultParts := strings.Fields(resultNorm)

	if len(searchedParts) < 2 || len(resultParts) < 2 {
		return false
	}

	searchedFirst := searchedParts[0]
	searchedLast := searchedParts[len(searchedParts)-1]
	resultFirst := resultParts[0]
	resultLast := resultParts[len(resultParts)-1]

	// Last names must match exactly
	if searchedLast != resultLast {
		return false
	}

	// First names: exact match or prefix match (for nicknames)
	if searchedFirst == resultFirst {
		return true
	}

	if strings.HasPrefix(searchedFirst, resultFirst) || strings.HasPrefix(resultFirst, searchedFirst) {
		return true
	}

	return false
}

// normalizeNameForVerification prepares a name for comparison.
func normalizeNameForVerification(name string) string {
	name = strings.TrimSpace(name)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, name)
	return strings.ToLower(result)
}
