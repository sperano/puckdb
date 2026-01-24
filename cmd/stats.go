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
	"github.com/sperano/puckdb/metrics"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	FlagStatsPort            = "port"
	FlagStatsRefreshInterval = "refresh-interval"

	defaultStatsPort            = 8790
	defaultStatsRefreshInterval = 5 * time.Minute
)

// cacheStats holds statistics for a single file type within a season
type cacheStats struct {
	seasonYear int
	fileType   cache.FileType
	expected   int
	found      int
}

func (s cacheStats) percentage() float64 {
	if s.expected == 0 {
		return 0
	}
	return float64(s.found) / float64(s.expected) * 100
}

type seasonResult struct {
	stats []cacheStats
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

func cmdStats() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "stats",
		Short: "Expose cache statistics as Prometheus metrics",
		Long: `Run a metrics server that periodically computes cache statistics
and exposes them via /metrics endpoint for Prometheus scraping.

Requires --data-path to locate cache files. Optionally provide --seasons
config file to also check Yahoo fantasy files.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
				return err
			}
			if err := viper.BindPFlag(FlagStatsPort, flags.Lookup(FlagStatsPort)); err != nil {
				return err
			}
			if err := viper.BindPFlag(FlagStatsRefreshInterval, flags.Lookup(FlagStatsRefreshInterval)); err != nil {
				return err
			}
			return config.BindSeasonRangeFlags(flags)
		},
		RunE: runStats,
	}
	flags := cmd.Flags()
	config.InitDataPathFlag(flags)
	config.InitSeasonRangeFlags(flags)
	flags.StringP(config.FlagYahooSeasons, "S", "yahoo-seasons.yaml", "Yahoo seasons config file (optional, enables Yahoo file checks)")
	flags.Int(FlagStatsPort, defaultStatsPort, "Port for metrics endpoint")
	flags.Duration(FlagStatsRefreshInterval, defaultStatsRefreshInterval, "Interval between stats refresh")
	return cmd
}

func runStats(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	port := viper.GetInt(FlagStatsPort)
	interval := viper.GetDuration(FlagStatsRefreshInterval)

	log.Info().
		Int("port", port).
		Dur("refresh_interval", interval).
		Str("data_path", viper.GetString(config.FlagDataPath)).
		Msg("Starting cache stats server")

	// Compute stats immediately on startup
	if err := computeAndUpdateStats(ctx); err != nil {
		log.Error().Err(err).Msg("Initial stats computation failed")
	}

	// Start ticker for periodic refresh
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := computeAndUpdateStats(ctx); err != nil {
					log.Error().Err(err).Msg("Stats computation failed")
				}
			}
		}
	}()

	// Start metrics server (blocks)
	addr := fmt.Sprintf(":%d", port)
	metrics.StartServer(addr)
	return nil
}

func computeAndUpdateStats(ctx context.Context) error {
	start := time.Now()

	stats, err := getAllStats(ctx)
	if err != nil {
		return err
	}

	// Update Prometheus gauges per season and file type
	for _, s := range stats {
		season := fmt.Sprintf("%d", s.seasonYear)
		fileType := cache.FileTypeName(s.fileType)
		metrics.SetCacheStats(season, fileType, s.expected, s.found)
	}

	// Compute and set totals
	var totalExpected, totalFound int
	for _, s := range stats {
		totalExpected += s.expected
		totalFound += s.found
	}
	metrics.SetCacheStats("total", "all", totalExpected, totalFound)

	metrics.SetCacheStatsTimestamp()
	metrics.SetCacheComputeDuration(time.Since(start))

	seasonCount := countUniqueSeasons(stats)
	log.Info().
		Int("seasons", seasonCount).
		Int("total_expected", totalExpected).
		Int("total_found", totalFound).
		Dur("duration", time.Since(start)).
		Msg("Cache stats updated")

	return nil
}

func countUniqueSeasons(stats []cacheStats) int {
	seen := make(map[int]bool)
	for _, s := range stats {
		seen[s.seasonYear] = true
	}
	return len(seen)
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

		start, err := time.Parse("2006-01-02", s.StandingsStart)
		if err != nil {
			log.Warn().Err(err).Int("season", startYear).Msg("Failed to parse standings start")
			continue
		}
		end, err := time.Parse("2006-01-02", s.StandingsEnd)
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

// getAllStats gathers cache statistics for all seasons
func getAllStats(ctx context.Context) ([]cacheStats, error) {
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
	yahooConfig, _ := config.GetSeasonsConfig()

	results := make(chan seasonResult, len(seasons))
	var wg sync.WaitGroup

	for _, season := range seasons {
		wg.Add(1)
		go func(s simpleSeason) {
			defer wg.Done()
			fs := cache.NewSimpleCache()

			// Always check NHL API files
			stats := checkNHLSeasonCache(fs, s)

			// If season is in Yahoo config, also check Yahoo fantasy files
			if yahooCfg, ok := yahooConfig[s.StartYear()]; ok {
				stats = append(stats, checkYahooSeasonCache(fs, s, yahooCfg)...)
			}

			results <- seasonResult{stats: stats, err: nil, year: s.StartYear()}
		}(season)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	allStats := make([]cacheStats, 0)
	for result := range results {
		if result.err != nil {
			log.Warn().Err(result.err).Int("season", result.year).Msg("Error checking season")
			continue
		}
		allStats = append(allStats, result.stats...)
	}
	return allStats, nil
}

// checkNHLSeasonCache checks NHL API files (daily schedule, boxscores)
func checkNHLSeasonCache(fs cache.FileSystem, season simpleSeason) []cacheStats {
	stats := make([]cacheStats, 0)
	seasonYear := season.StartYear()
	daysInSeason := countDays(season.start, season.end)

	// Count daily schedule files (1 per day)
	dailyScheduleStats := countDailyScheduleFilesSimple(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.DailyScheduleFileType,
		expected:   daysInSeason,
		found:      dailyScheduleStats,
	})

	// Count boxscore files (depends on daily-schedule files)
	expectedBoxscores, foundBoxscores := countBoxscoreFilesSimple(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.BoxscoreFileType,
		expected:   expectedBoxscores,
		found:      foundBoxscores,
	})

	return stats
}

// checkYahooSeasonCache checks Yahoo fantasy files (leagues, teams, rosters, summaries)
func checkYahooSeasonCache(fs cache.FileSystem, nhlSeason simpleSeason, yahooCfg config.Season) []cacheStats {
	stats := make([]cacheStats, 0)
	seasonYear := nhlSeason.startYear
	daysInSeason := countDays(nhlSeason.start, nhlSeason.end)

	// Count leagues
	leagueStats := countLeagueFiles(fs, seasonYear, yahooCfg)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.LeagueFileType,
		expected:   len(yahooCfg.Leagues),
		found:      leagueStats,
	})

	// Count teams
	totalTeams := 0
	for _, league := range yahooCfg.Leagues {
		totalTeams += len(league.TeamIDs)
	}
	teamStats := countTeamFiles(fs, seasonYear, yahooCfg)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.TeamFileType,
		expected:   totalTeams,
		found:      teamStats,
	})

	// Count rosters (1 per team per day)
	expectedRosters := totalTeams * daysInSeason
	rosterStats := countRosterFiles(fs, nhlSeason, yahooCfg)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.RosterFileType,
		expected:   expectedRosters,
		found:      rosterStats,
	})

	// Count team summaries (1 per team per day)
	expectedSummaries := totalTeams * daysInSeason
	summaryStats := countTeamSummaryFiles(fs, nhlSeason, yahooCfg)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.TeamSummaryFileType,
		expected:   expectedSummaries,
		found:      summaryStats,
	})

	return stats
}

func countDays(start, end time.Time) int {
	if end.After(time.Now()) {
		end = time.Now()
	}
	return int(end.Sub(start).Hours()/24) + 1
}

func countLeagueFiles(fs cache.FileSystem, seasonYear int, cfg config.Season) int {
	count := 0
	for _, league := range cfg.Leagues {
		file := cache.LeagueFile{Season: seasonYear, LeagueID: league.LeagueID}
		if fs.Exists(file) {
			count++
		}
	}
	return count
}

func countTeamFiles(fs cache.FileSystem, seasonYear int, cfg config.Season) int {
	count := 0
	for _, league := range cfg.Leagues {
		for _, teamID := range league.TeamIDs {
			file := cache.TeamFile{Season: seasonYear, LeagueID: league.LeagueID, TeamID: teamID}
			if fs.Exists(file) {
				count++
			}
		}
	}
	return count
}

func countRosterFiles(fs cache.FileSystem, nhlSeason simpleSeason, cfg config.Season) int {
	count := 0
	current := nhlSeason.start
	end := nhlSeason.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		for _, league := range cfg.Leagues {
			for _, teamID := range league.TeamIDs {
				file := fs.New(cache.RosterFileType, current, league.LeagueID, teamID)
				if fs.Exists(file) {
					count++
				}
			}
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

func countTeamSummaryFiles(fs cache.FileSystem, nhlSeason simpleSeason, cfg config.Season) int {
	count := 0
	current := nhlSeason.start
	end := nhlSeason.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		for _, league := range cfg.Leagues {
			for _, teamID := range league.TeamIDs {
				file := fs.New(cache.TeamSummaryFileType, current, league.LeagueID, teamID)
				if fs.Exists(file) {
					count++
				}
			}
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

func countDailyScheduleFilesSimple(fs cache.FileSystem, season simpleSeason) int {
	count := 0
	current := season.start
	end := season.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		file := fs.New(cache.DailyScheduleFileType, current)
		if fs.Exists(file) {
			count++
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

func countBoxscoreFilesSimple(fs cache.FileSystem, season simpleSeason) (expected int, found int) {
	current := season.start
	end := season.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		dailyScheduleFile := fs.New(cache.DailyScheduleFileType, current)
		if !fs.Exists(dailyScheduleFile) {
			current = current.AddDate(0, 0, 1)
			continue
		}

		gameIDs, err := cache.ParseDailySchedule(fs, dailyScheduleFile)
		if err != nil {
			log.Warn().Err(err).Time("date", current).Msg("Error parsing daily-schedule file")
			current = current.AddDate(0, 0, 1)
			continue
		}

		expected += len(gameIDs)

		for _, gameID := range gameIDs {
			boxscoreFile := fs.New(cache.BoxscoreFileType, current, gameID)
			if fs.Exists(boxscoreFile) {
				found++
			}
		}

		current = current.AddDate(0, 0, 1)
	}

	return expected, found
}
