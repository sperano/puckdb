package cmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
)

// cacheMetrics holds statistics for a single file type within a season
type cacheMetrics struct {
	seasonYear int
	fileType   string
	expected   int
	found      int
}

func (s cacheMetrics) percentage() float64 {
	if s.expected == 0 {
		return 0
	}
	return float64(s.found) / float64(s.expected) * 100
}

type seasonResult struct {
	stats []cacheMetrics
	err   error
	year  int
}

// simpleSeason represents a season with just dates for NHL API-based checks
type simpleSeason struct {
	startYear int
	start     time.Time
	end       time.Time
}

func (s simpleSeason) StartYear() int {
	return s.startYear
}

// fetchSeasonsFromNHL fetches season metadata from the NHL API
func fetchSeasonsFromNHL(ctx context.Context) ([]simpleSeason, error) {
	client := nhl.NewClient()
	seasons, err := client.SeasonStandingManifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch seasons from NHL API: %w", err)
	}

	startFilter, endFilter := config.GetSeasonRange()

	var result []simpleSeason
	for _, s := range seasons {
		startYear := s.ID.StartYear()

		if startFilter > 0 && startYear < startFilter {
			continue
		}
		if endFilter > 0 && startYear > endFilter {
			continue
		}

		start, err := time.Parse(config.DateFormat, s.StandingsStart)
		if err != nil {
			log.Warn().Err(err).Int("season", startYear).Msg("Failed to parse standings start")
			continue
		}
		end, err := time.Parse(config.DateFormat, s.StandingsEnd)
		if err != nil {
			log.Warn().Err(err).Int("season", startYear).Msg("Failed to parse standings end")
			continue
		}

		result = append(result, simpleSeason{
			startYear: startYear,
			start:     start,
			end:       end,
		})
	}

	return result, nil
}

// getAllMetrics gathers cache statistics for all seasons
func getAllMetrics(ctx context.Context, redisClient cache.Client) ([]cacheMetrics, error) {
	if viper.GetString(config.FlagDataPath) == "" {
		return nil, fmt.Errorf("data-path is required")
	}

	// Fetch seasons from NHL API based on season flags
	seasons, err := fetchSeasonsFromNHL(ctx)
	if err != nil {
		return nil, err
	}

	if len(seasons) == 0 {
		return nil, fmt.Errorf("no seasons found matching the specified range")
	}

	// Load Yahoo config (optional - for checking Yahoo files)
	yahooConfig, _ := config.GetYahooSeasonsConfig()

	results := make(chan seasonResult, len(seasons))
	var wg sync.WaitGroup

	for _, season := range seasons {
		wg.Add(1)
		go func(s simpleSeason) {
			defer wg.Done()
			fs := store.NewStore()

			// Always check NHL API files
			cacheData := checkNHLSeasonCache(context.Background(), fs, redisClient, s)

			// If season is in Yahoo config, also check Yahoo fantasy files
			if yahooCfg, ok := yahooConfig[s.StartYear()]; ok {
				cacheData = append(cacheData, checkYahooSeasonCache(fs, s, yahooCfg)...)
			}

			results <- seasonResult{stats: cacheData, err: nil, year: s.StartYear()}
		}(season)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	allMetrics := make([]cacheMetrics, 0)
	for result := range results {
		if result.err != nil {
			log.Warn().Err(result.err).Int("season", result.year).Msg("Error checking season")
			continue
		}
		allMetrics = append(allMetrics, result.stats...)
	}
	return allMetrics, nil
}

// checkNHLSeasonCache checks NHL API files (daily schedule, boxscores, play-by-play, shift charts)
func checkNHLSeasonCache(ctx context.Context, fs store.Store, redisClient cache.Client, season simpleSeason) []cacheMetrics {
	cacheData := make([]cacheMetrics, 0)
	seasonYear := season.StartYear()
	daysInSeason := countDays(season.start, season.end)

	// Count daily schedule files (1 per day)
	dailyScheduleCount := countDailyScheduleFilesSimple(fs, season)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   store.FileTypeDailySchedule,
		expected:   daysInSeason,
		found:      dailyScheduleCount,
	})

	// Count game data files (boxscore, play-by-play, shift chart)
	gameFileCounts := countGameFilesSimple(ctx, fs, redisClient, season)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   store.FileTypeBoxscore,
		expected:   gameFileCounts.expectedGames,
		found:      gameFileCounts.boxscores,
	})
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   store.FileTypePlayByPlay,
		expected:   gameFileCounts.expectedGames,
		found:      gameFileCounts.playByPlay,
	})
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   store.FileTypeShiftChart,
		expected:   gameFileCounts.expectedGames,
		found:      gameFileCounts.shiftCharts,
	})

	return cacheData
}

// checkYahooSeasonCache checks Yahoo fantasy files (leagues, teams, rosters, summaries)
func checkYahooSeasonCache(fs store.Store, nhlSeason simpleSeason, yahooCfg config.Season) []cacheMetrics {
	cacheData := make([]cacheMetrics, 0)
	seasonYear := nhlSeason.startYear
	daysInSeason := countDays(nhlSeason.start, nhlSeason.end)

	// Count leagues
	leagueCount := countLeagueFiles(fs, seasonYear, yahooCfg)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   store.FileTypeLeague,
		expected:   len(yahooCfg.Leagues),
		found:      leagueCount,
	})

	// Count teams
	totalTeams := 0
	for _, league := range yahooCfg.Leagues {
		totalTeams += len(league.TeamIDs)
	}
	teamCount := countTeamFiles(fs, seasonYear, yahooCfg)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   store.FileTypeTeam,
		expected:   totalTeams,
		found:      teamCount,
	})

	// Count rosters (1 per team per day)
	expectedRosters := totalTeams * daysInSeason
	rosterCount := countRosterFiles(fs, nhlSeason, yahooCfg)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   store.FileTypeRoster,
		expected:   expectedRosters,
		found:      rosterCount,
	})

	// Count team summaries (1 per team per day)
	expectedSummaries := totalTeams * daysInSeason
	summaryCount := countTeamSummaryFiles(fs, nhlSeason, yahooCfg)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   store.FileTypeTeamSummary,
		expected:   expectedSummaries,
		found:      summaryCount,
	})

	return cacheData
}

func countDays(start, end time.Time) int {
	if end.After(time.Now()) {
		end = time.Now()
	}
	return int(end.Sub(start).Hours()/24) + 1
}

func countLeagueFiles(fs store.Store, seasonYear int, cfg config.Season) int {
	count := 0
	for _, league := range cfg.Leagues {
		file := store.LeagueFile{Season: seasonYear, LeagueID: league.LeagueID}
		if fs.Exists(file) {
			count++
		}
	}
	return count
}

func countTeamFiles(fs store.Store, seasonYear int, cfg config.Season) int {
	count := 0
	for _, league := range cfg.Leagues {
		for _, teamID := range league.TeamIDs {
			file := store.TeamFile{Season: seasonYear, LeagueID: league.LeagueID, TeamID: teamID}
			if fs.Exists(file) {
				count++
			}
		}
	}
	return count
}

func countRosterFiles(fs store.Store, nhlSeason simpleSeason, cfg config.Season) int {
	count := 0
	current := nhlSeason.start
	end := nhlSeason.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		for _, league := range cfg.Leagues {
			for _, teamID := range league.TeamIDs {
				file := store.RosterFile{Date: current, LeagueID: league.LeagueID, TeamID: teamID}
				if fs.Exists(file) {
					count++
				}
			}
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

func countTeamSummaryFiles(fs store.Store, nhlSeason simpleSeason, cfg config.Season) int {
	count := 0
	current := nhlSeason.start
	end := nhlSeason.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		for _, league := range cfg.Leagues {
			for _, teamID := range league.TeamIDs {
				file := store.TeamSummaryFile{Date: current, LeagueID: league.LeagueID, TeamID: teamID}
				if fs.Exists(file) {
					count++
				}
			}
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

func countDailyScheduleFilesSimple(fs store.Store, season simpleSeason) int {
	count := 0
	current := season.start
	end := season.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		file := store.DailyScheduleFile{Date: current}
		if fs.Exists(file) {
			count++
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

// gameFileCounts holds counts for all game-related file types
type gameFileCounts struct {
	expectedGames int
	boxscores     int
	playByPlay    int
	shiftCharts   int
}

func countGameFilesSimple(ctx context.Context, fs store.Store, redisClient cache.Client, season simpleSeason) gameFileCounts {
	counts := gameFileCounts{}
	current := season.start
	end := season.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		dailyScheduleFile := store.DailyScheduleFile{Date: current}
		if !fs.Exists(dailyScheduleFile) {
			current = current.AddDate(0, 0, 1)
			continue
		}

		gameIDs, err := cache.GetGameIds(ctx, fs, redisClient, dailyScheduleFile)
		if err != nil {
			log.Warn().Err(err).Time("date", current).Msg("Error parsing daily-schedule file")
			current = current.AddDate(0, 0, 1)
			continue
		}

		// Filter out preseason games
		gameIDs = filterRegularSeasonGames(gameIDs)

		counts.expectedGames += len(gameIDs)

		for _, gameID := range gameIDs {
			if fs.Exists(store.BoxscoreFile{Date: current, GameID: gameID}) {
				counts.boxscores++
			}
			if fs.Exists(store.PlayByPlayFile{Date: current, GameID: gameID}) {
				counts.playByPlay++
			}
			if fs.Exists(store.ShiftChartFile{Date: current, GameID: gameID}) {
				counts.shiftCharts++
			}
		}

		current = current.AddDate(0, 0, 1)
	}

	return counts
}

// filterRegularSeasonGames filters out preseason games from a list of game IDs.
func filterRegularSeasonGames(gameIDs []nhl.GameID) []nhl.GameID {
	result := make([]nhl.GameID, 0, len(gameIDs))
	for _, id := range gameIDs {
		gameType, err := id.GameType()
		if err != nil {
			log.Warn().Str("gameid", id.String()).Err(err).Msg("Skipping game with invalid ID")
			continue
		}
		if nhl.GameType(gameType) == nhl.GameTypePreseason {
			continue
		}
		result = append(result, id)
	}
	return result
}

func countUniqueSeasons(cacheData []cacheMetrics) int {
	seen := make(map[int]bool)
	for _, s := range cacheData {
		seen[s.seasonYear] = true
	}
	return len(seen)
}
