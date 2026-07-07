package player

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
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

// heartbeatFunc records progress on a long-running activity so Temporal
// knows the worker is still alive. The default implementation calls
// activity.RecordHeartbeat, which requires a real Temporal activity
// context; tests substitute a no-op so verifyUnmatchedBatch can be
// exercised with a plain context.Context.
type heartbeatFunc func(ctx context.Context, details ...any)

func defaultHeartbeat(ctx context.Context, details ...any) {
	activity.RecordHeartbeat(ctx, details...)
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

// VerifyUnmatchedBatch verifies a batch of unmatched Yahoo players against the NHL API.
// This is called multiple times by the workflow to enable progress tracking.
// It categorizes players into:
// - VerifiedNonNHL: confirmed 0 NHL games (can be ignored in future)
// - TrulyUnmatched: have NHL games but weren't matched (need investigation)
// - NotFoundInNHL: no matching name in NHL database
func (a *Activities) VerifyUnmatchedBatch(ctx context.Context, players []UnmatchedYahooPlayer) (*VerifyUnmatchedResult, error) {
	logger := activity.GetLogger(ctx)

	result, err := a.verifyUnmatchedBatchImpl(ctx, players)
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

// verifyUnmatchedBatchImpl contains the testable logic for VerifyUnmatchedBatch.
func (a *Activities) verifyUnmatchedBatchImpl(ctx context.Context, players []UnmatchedYahooPlayer) (*VerifyUnmatchedResult, error) {
	return a.verifyUnmatchedBatch(ctx, players, defaultHeartbeat)
}

// verifyUnmatchedBatch is the heartbeat-injectable implementation behind
// verifyUnmatchedBatchImpl. It checks ctx.Done() and records a heartbeat on
// every iteration, and waits out verifyAPIDelay between NHL API calls with a
// cancellable select instead of a blocking time.Sleep, so a workflow/worker
// shutdown or activity cancellation is honored promptly instead of stalling
// until the whole batch finishes (or, worst case, until the heartbeat
// timeout fires and Temporal retries the activity).
func (a *Activities) verifyUnmatchedBatch(ctx context.Context, players []UnmatchedYahooPlayer, heartbeat heartbeatFunc) (*VerifyUnmatchedResult, error) {
	alreadyVerified, err := LoadVerifiedNonNHLIDs(ctx, a.RedisClient)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load verified non-NHL IDs, will verify all")
		alreadyVerified = make(map[store.YahooPlayerID]struct{})
	}

	result := &VerifyUnmatchedResult{
		VerifiedNonNHL: make([]store.YahooPlayerID, 0),
		TrulyUnmatched: make([]VerifiedPlayer, 0),
		NotFoundInNHL:  make([]VerifiedPlayer, 0),
	}

	newlyVerifiedNonNHL := make([]store.YahooPlayerID, 0)

	for i, player := range players {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		heartbeat(ctx, player.YahooID)

		if _, ok := alreadyVerified[player.YahooID]; ok {
			result.VerifiedNonNHL = append(result.VerifiedNonNHL, player.YahooID)
			continue
		}

		verified := verifyPlayer(ctx, a.NHLClient, a.Storage, a.GobCache, player)

		if !verified.FoundInNHL {
			result.NotFoundInNHL = append(result.NotFoundInNHL, verified)
			newlyVerifiedNonNHL = append(newlyVerifiedNonNHL, player.YahooID)
		} else if verified.HasNHLGames {
			result.TrulyUnmatched = append(result.TrulyUnmatched, verified)
		} else {
			result.VerifiedNonNHL = append(result.VerifiedNonNHL, player.YahooID)
			newlyVerifiedNonNHL = append(newlyVerifiedNonNHL, player.YahooID)
		}

		if i < len(players)-1 {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(verifyAPIDelay):
			}
		}
	}

	if len(newlyVerifiedNonNHL) > 0 {
		if err := SaveVerifiedNonNHLIDs(ctx, a.RedisClient, newlyVerifiedNonNHL); err != nil {
			log.Warn().Err(err).Msg("Failed to save verified non-NHL IDs to Redis")
		}
	}

	return result, nil
}

// verifyPlayer checks if a Yahoo player has any NHL regular season games.
func verifyPlayer(ctx context.Context, client shared.NHLClient, storage store.Storage, gobCache *cache.GobCache, player UnmatchedYahooPlayer) VerifiedPlayer {
	result := VerifiedPlayer{
		YahooID:   player.YahooID,
		FirstName: player.FirstName,
		LastName:  player.LastName,
	}

	playerName := player.FirstName + " " + player.LastName

	searchResults, err := client.SearchPlayer(ctx, playerName, maxSearchResults)
	if err != nil {
		log.Warn().Err(err).Str("name", playerName).Msg("Search failed")
		return result
	}

	if len(searchResults) == 0 {
		return result
	}

	matchedResult := findMatchingPlayer(playerName, searchResults)
	if matchedResult == nil {
		return result
	}

	result.FoundInNHL = true
	result.NHLPlayerID = matchedResult.PlayerID.Int64()
	result.NHLName = matchedResult.Name

	landing, err := fetchPlayerLanding(ctx, client, storage, gobCache, matchedResult.PlayerID)
	if err != nil {
		log.Warn().Err(err).Str("name", playerName).Msg("Failed to get landing data")
		return result
	}

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
func fetchPlayerLanding(ctx context.Context, client shared.NHLClient, storage store.Storage, gobCache *cache.GobCache, playerID nhl.PlayerID) (*nhl.PlayerLanding, error) {
	landingRes := resource.PlayerLanding{PlayerID: playerID}
	if storage.Exists(ctx, landingRes.Path()) {
		landing, _, err := cache.ReadParsedCached(ctx, storage, gobCache, landingRes)
		if err == nil {
			return landing, nil
		}
	}

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

	if searchedNorm == resultNorm {
		return true
	}

	searchedParts := strings.Fields(searchedNorm)
	resultParts := strings.Fields(resultNorm)

	if len(searchedParts) < 2 || len(resultParts) < 2 {
		return false
	}

	searchedFirst := searchedParts[0]
	searchedLast := searchedParts[len(searchedParts)-1]
	resultFirst := resultParts[0]
	resultLast := resultParts[len(resultParts)-1]

	if searchedLast != resultLast {
		return false
	}

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
