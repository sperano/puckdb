package worker

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
)

// BoxscorePlayer holds minimal player info extracted from boxscore appearances.
// This is used to carry player data from extraction to download phases.
type BoxscorePlayer struct {
	ID        int64  `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Position  string `json:"position"`
}

// BoxscoreExtractionResult contains players extracted from boxscores for a season.
type BoxscoreExtractionResult struct {
	Players []BoxscorePlayer `json:"players"`
}

// ExtractBoxscoreDataForSeasonActivity extracts player info from all boxscores for a season.
func ExtractBoxscoreDataForSeasonActivity(ctx context.Context, season SeasonInfo) (BoxscoreExtractionResult, error) {
	return extractBoxscoreDataForSeasonImpl(ctx, store.NewStore(), cache.NewClient(), season)
}

func extractBoxscoreDataForSeasonImpl(
	ctx context.Context,
	fs store.Store,
	redisClient cache.Client,
	season SeasonInfo,
) (BoxscoreExtractionResult, error) {
	// Map by player ID to deduplicate while preserving player info
	players := make(map[int64]BoxscorePlayer)

	end := season.EndDate
	if end.After(time.Now()) {
		end = time.Now()
	}

	dayCount := 0
	for day := season.StartDate; !day.After(end); day = day.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return BoxscoreExtractionResult{}, ctx.Err()
		default:
		}

		dayPlayers, err := extractPlayersForDay(ctx, fs, redisClient, day)
		if err != nil {
			log.Debug().Err(err).Time("day", day).Msg("Failed to extract boxscore data for day")
			continue
		}

		for _, p := range dayPlayers {
			players[p.ID] = p
		}

		dayCount++
		if dayCount%30 == 0 {
			log.Debug().
				Int("season", season.StartYear).
				Int("days_processed", dayCount).
				Int("unique_players", len(players)).
				Msg("Season extraction progress")
		}
	}

	// Convert map to slice
	playerSlice := make([]BoxscorePlayer, 0, len(players))
	for _, p := range players {
		playerSlice = append(playerSlice, p)
	}

	log.Info().
		Int("season", season.StartYear).
		Int("days_processed", dayCount).
		Int("unique_players", len(playerSlice)).
		Msg("Season extraction complete")

	return BoxscoreExtractionResult{
		Players: playerSlice,
	}, nil
}

// extractPlayersForDay extracts player info from all boxscores for a single day.
func extractPlayersForDay(
	ctx context.Context,
	fs store.Store,
	redisClient cache.Client,
	day time.Time,
) ([]BoxscorePlayer, error) {
	boxscoreFiles, err := getBoxscoreFilesForDay(ctx, fs, redisClient, day)
	if err != nil {
		return nil, err
	}

	var players []BoxscorePlayer

	for _, file := range boxscoreFiles {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if !fs.Exists(file) {
			continue
		}

		content, err := fs.Read(file)
		if err != nil {
			continue
		}

		var boxscore nhl.Boxscore
		if err := json.Unmarshal(content, &boxscore); err != nil {
			continue
		}

		// Extract players from both teams
		players = append(players, extractTeamPlayers(&boxscore.PlayerByGameStats.HomeTeam)...)
		players = append(players, extractTeamPlayers(&boxscore.PlayerByGameStats.AwayTeam)...)
	}

	return players, nil
}

// extractTeamPlayers extracts all players from a team's player stats.
func extractTeamPlayers(stats *nhl.TeamPlayerStats) []BoxscorePlayer {
	var players []BoxscorePlayer

	for _, s := range stats.Forwards {
		first, last := parseCombinedName(s.Name.String())
		players = append(players, BoxscorePlayer{
			ID:        s.PlayerID.AsInt64(),
			FirstName: first,
			LastName:  last,
			Position:  string(s.Position),
		})
	}
	for _, s := range stats.Defense {
		first, last := parseCombinedName(s.Name.String())
		players = append(players, BoxscorePlayer{
			ID:        s.PlayerID.AsInt64(),
			FirstName: first,
			LastName:  last,
			Position:  string(s.Position),
		})
	}
	for _, g := range stats.Goalies {
		first, last := parseCombinedName(g.Name.String())
		players = append(players, BoxscorePlayer{
			ID:        g.PlayerID.AsInt64(),
			FirstName: first,
			LastName:  last,
			Position:  string(g.Position),
		})
	}

	return players
}

// parseCombinedName splits a combined name like "Connor McDavid" into first and last name.
// For multi-part names, the first token is the first name and the rest is the last name.
// Examples:
//   - "Connor McDavid" -> ("Connor", "McDavid")
//   - "Pierre-Luc Dubois" -> ("Pierre-Luc", "Dubois")
//   - "James van Riemsdyk" -> ("James", "van Riemsdyk")
func parseCombinedName(fullName string) (firstName, lastName string) {
	parts := strings.SplitN(strings.TrimSpace(fullName), " ", 2)
	if len(parts) >= 1 {
		firstName = parts[0]
	}
	if len(parts) >= 2 {
		lastName = parts[1]
	}
	return firstName, lastName
}
