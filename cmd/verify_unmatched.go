package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/spf13/cobra"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	// Rate limiting delay between API calls to avoid hammering the NHL API
	apiCallDelay = 500 * time.Millisecond
	// Maximum number of search results to consider
	maxSearchResults = 20
	// League abbreviation for NHL
	leagueNHL = "NHL"
)

func cmdVerifyUnmatched() *cobra.Command {
	var inputFile string

	cmd := &cobra.Command{
		Use:   "verify-unmatched",
		Short: "Verify NHL game history for unmatched players",
		Long: `Reads player names from a file and checks their NHL regular season game history.
Each line should be formatted like: "  - Dan DaSilva (#0, ) [Yahoo ID: 3845]"
The tool extracts player names, searches the NHL API, and reports their NHL game totals.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})
			return runVerifyUnmatched(cmd.Context(), inputFile, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVarP(&inputFile, "file", "f", "/Users/eric/projects/puckdb/unmatched.txt", "Path to unmatched players file")
	return cmd
}

// playerResult holds the verification results for a single player
type playerResult struct {
	name          string
	found         bool
	matchedName   string // The actual NHL player name that was matched
	nhlGames      int
	hasNHLGames   bool
	errorOccurred bool
	errorMessage  string
}

// normalizeName prepares a name for comparison by:
// 1. Trimming whitespace
// 2. Removing diacritics/accents (é -> e, ü -> u)
// 3. Converting to lowercase
func normalizeName(name string) string {
	name = strings.TrimSpace(name)

	// Remove diacritics using unicode normalization
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, name)

	return strings.ToLower(result)
}

// extractLastName returns the last word of a name (handles multi-part names)
func extractLastName(fullName string) string {
	parts := strings.Fields(fullName)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// namesMatch checks if two player names refer to the same person.
// Requires last names to match exactly (after normalization).
// First names must either match exactly or one must be a prefix of the other
// (to handle nicknames like "Mike" vs "Michael").
func namesMatch(searchedName, resultName string) bool {
	searchedNorm := normalizeName(searchedName)
	resultNorm := normalizeName(resultName)

	// Extract first and last names
	searchedParts := strings.Fields(searchedNorm)
	resultParts := strings.Fields(resultNorm)

	if len(searchedParts) < 2 || len(resultParts) < 2 {
		// Can't compare properly without both first and last name
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

	// Allow prefix matching for common nickname patterns (Mike/Michael, etc.)
	if strings.HasPrefix(searchedFirst, resultFirst) || strings.HasPrefix(resultFirst, searchedFirst) {
		return true
	}

	return false
}

func runVerifyUnmatched(ctx context.Context, inputFile string, output interface{ Write([]byte) (int, error) }) error {
	// Read and parse the input file
	players, err := parseUnmatchedFile(inputFile)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	log.Info().Msgf("Found %d players to verify", len(players))

	// Create NHL API client
	client := nhl.NewClient()

	// Process each player
	results := make([]playerResult, 0, len(players))
	for i, playerName := range players {
		log.Info().Msgf("[%d/%d] Checking %s...", i+1, len(players), playerName)

		result := verifyPlayer(ctx, client, playerName)
		results = append(results, result)

		// Rate limiting: sleep between API calls (except after the last one)
		if i < len(players)-1 {
			time.Sleep(apiCallDelay)
		}
	}

	// Print summary
	printResults(output, results)

	return nil
}

// parseUnmatchedFile reads the file and extracts player names from each line
func parseUnmatchedFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	// Regular expression to extract player name from format:
	// "  - Dan DaSilva (#0, ) [Yahoo ID: 3845]"
	// Captures the name between "- " and " (#"
	namePattern := regexp.MustCompile(`^\s*-\s+([^(]+?)\s+\(#`)

	var players []string
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}

		matches := namePattern.FindStringSubmatch(line)
		if len(matches) < 2 {
			log.Warn().Msgf("Line %d: Could not parse player name: %q", lineNum, line)
			continue
		}

		playerName := strings.TrimSpace(matches[1])
		players = append(players, playerName)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return players, nil
}

// verifyPlayer searches for a player and calculates their NHL regular season games
func verifyPlayer(ctx context.Context, client *nhl.Client, playerName string) playerResult {
	result := playerResult{
		name:  playerName,
		found: false,
	}

	// Search for the player
	limit := maxSearchResults
	searchResults, err := client.SearchPlayer(ctx, playerName, &limit)
	if err != nil {
		result.errorOccurred = true
		result.errorMessage = fmt.Sprintf("search failed: %v", err)
		log.Error().Err(err).Msgf("Failed to search for %s", playerName)
		return result
	}

	// If no results found, return early
	if len(searchResults) == 0 {
		log.Debug().Msgf("No search results for %s", playerName)
		return result
	}

	// Find the first result where the name actually matches
	var matchedResult *nhl.PlayerSearchResult
	for i := range searchResults {
		sr := &searchResults[i]
		if namesMatch(playerName, sr.Name) {
			matchedResult = sr
			log.Debug().
				Str("searched", playerName).
				Str("matched", sr.Name).
				Msgf("Found name match")
			break
		}
	}

	// If no matching name found, log what we did find
	if matchedResult == nil {
		log.Debug().
			Str("searched", playerName).
			Str("first_result", searchResults[0].Name).
			Int("total_results", len(searchResults)).
			Msgf("No name match found (first result was different player)")
		return result
	}

	result.found = true
	result.matchedName = matchedResult.Name

	// Get full player landing data
	landing, err := client.PlayerLanding(ctx, matchedResult.PlayerID)
	if err != nil {
		result.errorOccurred = true
		result.errorMessage = fmt.Sprintf("landing data failed: %v", err)
		log.Error().Err(err).Msgf("Failed to get landing data for %s (ID: %s)", playerName, matchedResult.PlayerID.String())
		return result
	}

	// Calculate total NHL regular season games
	nhlGames := 0
	if landing.SeasonTotals != nil {
		for _, season := range landing.SeasonTotals {
			// Only count NHL regular season games (GameType == 2)
			if season.LeagueAbbrev == leagueNHL && season.GameType == nhl.GameTypeRegularSeason {
				nhlGames += season.GamesPlayed
			}
		}
	}

	result.nhlGames = nhlGames
	result.hasNHLGames = nhlGames > 0

	return result
}

// printResults outputs a formatted summary of all verification results
func printResults(output interface{ Write([]byte) (int, error) }, results []playerResult) {
	fmt.Fprintln(output)
	fmt.Fprintln(output, "═══════════════════════════════════════════════════════════════")
	fmt.Fprintln(output, "           Unmatched Players Verification Report")
	fmt.Fprintln(output, "═══════════════════════════════════════════════════════════════")
	fmt.Fprintln(output)

	// Categorize results
	var notFound []playerResult
	var zeroGames []playerResult
	var hasGames []playerResult
	var errors []playerResult

	for _, r := range results {
		if r.errorOccurred {
			errors = append(errors, r)
		} else if !r.found {
			notFound = append(notFound, r)
		} else if r.hasNHLGames {
			hasGames = append(hasGames, r)
		} else {
			zeroGames = append(zeroGames, r)
		}
	}

	// Print summary statistics
	fmt.Fprintf(output, "Total players checked: %d\n", len(results))
	fmt.Fprintf(output, "  ├─ No matching name in NHL: %d\n", len(notFound))
	fmt.Fprintf(output, "  ├─ Found, 0 NHL games: %d ✓\n", len(zeroGames))
	fmt.Fprintf(output, "  ├─ Found with NHL games: %d ⚠️\n", len(hasGames))
	fmt.Fprintf(output, "  └─ Errors during lookup: %d\n", len(errors))
	fmt.Fprintln(output)

	// Print players with NHL games (these are exceptions that need investigation)
	if len(hasGames) > 0 {
		fmt.Fprintln(output, "⚠️  PLAYERS WITH NHL GAMES (NEED INVESTIGATION) ⚠️")
		fmt.Fprintln(output, "───────────────────────────────────────────────────")
		for _, r := range hasGames {
			if r.matchedName != "" && r.matchedName != r.name {
				fmt.Fprintf(output, "  • %s → %s: %d NHL games\n", r.name, r.matchedName, r.nhlGames)
			} else {
				fmt.Fprintf(output, "  • %s: %d NHL games\n", r.name, r.nhlGames)
			}
		}
		fmt.Fprintln(output)
	}

	// Print not found players (no exact name match in NHL database)
	if len(notFound) > 0 {
		fmt.Fprintln(output, "NO MATCHING NAME IN NHL DATABASE")
		fmt.Fprintln(output, "───────────────────────────────────────────────────")
		for _, r := range notFound {
			fmt.Fprintf(output, "  • %s\n", r.name)
		}
		fmt.Fprintln(output)
	}

	// Print error cases
	if len(errors) > 0 {
		fmt.Fprintln(output, "ERRORS DURING LOOKUP")
		fmt.Fprintln(output, "───────────────────────────────────────────────────")
		for _, r := range errors {
			fmt.Fprintf(output, "  • %s: %s\n", r.name, r.errorMessage)
		}
		fmt.Fprintln(output)
	}

	// Print verified with zero games (for completeness)
	if len(zeroGames) > 0 {
		fmt.Fprintln(output, "VERIFIED - 0 NHL GAMES (AS EXPECTED)")
		fmt.Fprintln(output, "───────────────────────────────────────────────────")
		for _, r := range zeroGames {
			if r.matchedName != "" && r.matchedName != r.name {
				fmt.Fprintf(output, "  • %s → %s\n", r.name, r.matchedName)
			} else {
				fmt.Fprintf(output, "  • %s\n", r.name)
			}
		}
		fmt.Fprintln(output)
	}

	fmt.Fprintln(output, "═══════════════════════════════════════════════════════════════")
}
