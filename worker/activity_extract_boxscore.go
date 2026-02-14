package worker

import (
	"context"
	"strings"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/store"
)

// ExtractBoxscorePlayersForDayActivity extracts players from all boxscore files for one day.
//func ExtractBoxscorePlayersForDayActivity(ctx context.Context, day time.Time) (map[int64]PartialPlayer, error) {
//	players := make(map[int64]PartialPlayer)
//	fs := cache.NewFileSystem()
//
//	// Get list of boxscore files for this day via daily schedule
//	boxscoreFiles, err := getBoxscoreFilesForDay(fs, day)
//	if err != nil {
//		log.Debug().Time("day", day).Msg("No daily schedule for day")
//		return players, nil
//	}
//
//	for _, file := range boxscoreFiles {
//		select {
//		case <-ctx.Done():
//			return nil, ctx.Err()
//		default:
//		}
//
//		if !fs.Exists(file) {
//			continue
//		}
//
//		content, err := fs.Read(file)
//		if err != nil {
//			log.Warn().Err(err).Str("file", cache.Path(file)).Msg("Failed to read boxscore")
//			continue
//		}
//
//		var boxscore nhl.Boxscore
//		if err := json.Unmarshal(content, &boxscore); err != nil {
//			log.Warn().Err(err).Str("file", cache.Path(file)).Msg("Failed to parse boxscore")
//			continue
//		}
//
//		// Extract from home team
//		extractTeamPlayers(players, &boxscore.PlayerByGameStats.HomeTeam, int64(boxscore.HomeTeam.ID))
//		// Extract from away team
//		extractTeamPlayers(players, &boxscore.PlayerByGameStats.AwayTeam, int64(boxscore.AwayTeam.ID))
//	}
//
//	log.Debug().Time("day", day).Int("players", len(players)).Msg("Boxscore extraction for day complete")
//	return players, nil
//}

//func extractTeamPlayers(players map[int64]PartialPlayer, stats *nhl.TeamPlayerStats, teamID int64) {
//	// Forwards
//	for _, s := range stats.Forwards {
//		addSkaterPlayer(players, s, teamID)
//	}
//	// Defense
//	for _, s := range stats.Defense {
//		addSkaterPlayer(players, s, teamID)
//	}
//	// Goalies
//	for _, g := range stats.Goalies {
//		addGoaliePlayer(players, g, teamID)
//	}
//}

func addSkaterPlayer(players map[int64]PartialPlayer, s nhl.SkaterStats, teamID int64) {
	id := s.PlayerID.AsInt64()
	firstName, lastName := parseLocalizedName(s.Name)

	players[id] = PartialPlayer{
		ID:              id,
		FirstName:       firstName,
		LastName:        lastName,
		Position:        string(s.Position),
		SweaterNumber:   s.SweaterNumber,
		TeamID:          teamID,
		HasBoxscoreData: true,
	}
}

func addGoaliePlayer(players map[int64]PartialPlayer, g nhl.GoalieStats, teamID int64) {
	id := g.PlayerID.AsInt64()
	firstName, lastName := parseLocalizedName(g.Name)

	players[id] = PartialPlayer{
		ID:              id,
		FirstName:       firstName,
		LastName:        lastName,
		Position:        string(g.Position),
		SweaterNumber:   g.SweaterNumber,
		TeamID:          teamID,
		HasBoxscoreData: true,
	}
}

// parseLocalizedName splits "FirstName LastName" from nhl.LocalizedString.
func parseLocalizedName(name nhl.LocalizedString) (first, last string) {
	parts := strings.SplitN(name.Default, " ", 2)
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return name.Default, ""
}

// getBoxscoreFilesForDay returns boxscore files for a specific day.
func getBoxscoreFilesForDay(ctx context.Context, fs store.Store, redisClient cache.Client, day time.Time) ([]store.File, error) {
	// First get the daily schedule to know which game IDs exist
	scheduleFile := store.DailyScheduleFile{Date: day}
	if !fs.Exists(scheduleFile) {
		return nil, nil
	}

	gameIDs, err := cache.GetGameIds(ctx, fs, redisClient, scheduleFile)
	if err != nil {
		return nil, err
	}

	files := make([]store.File, 0, len(gameIDs))
	for _, id := range gameIDs {
		file := store.BoxscoreFile{Date: day, GameID: id}
		files = append(files, file)
	}

	return files, nil
}
