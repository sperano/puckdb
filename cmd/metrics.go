package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/redis"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// Local flag name for port (aliased from config.FlagMetricsPort for the metrics command)
const FlagMetricsPortLocal = "port"

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

func cmdMetrics() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "metrics",
		Short: "Expose cache, Redis, and database metrics as Prometheus metrics",
		Long: `Run a metrics server that periodically computes various metrics
and exposes them via /metrics endpoint for Prometheus scraping.

Collectors:
  - Cache: File cache completeness statistics (requires --data-path)
  - Redis: OAuth token existence check (requires --redis-url)
  - Database: Table row counts (requires --postgres-* flags)

Each collector runs independently at its own interval.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagMetricsPort, flags.Lookup(FlagMetricsPortLocal)); err != nil {
				return err
			}
			if err := config.BindCacheIntervalSecondsFlag(flags); err != nil {
				return err
			}
			if err := config.BindRedisIntervalSecondsFlag(flags); err != nil {
				return err
			}
			if err := config.BindDBIntervalSecondsFlag(flags); err != nil {
				return err
			}
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			if err := config.BindPostgresFlags(flags); err != nil {
				return err
			}
			if err := config.BindGameIDCacheTTLFlag(flags); err != nil {
				return err
			}
			return config.BindSeasonRangeFlags(flags)
		},
		RunE: runMetrics,
	}
	flags := cmd.Flags()
	config.InitDataPathFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitRedisFlags(flags)
	config.InitPostgresFlags(flags)
	config.InitGameIDCacheTTLFlag(flags)
	flags.StringP(config.FlagYahooSeasons, "S", config.DefaultYahooSeasonsFile, "Yahoo seasons config file (optional, enables Yahoo file checks)")
	flags.Int(FlagMetricsPortLocal, config.DefaultMetricsPort, "Port for metrics endpoint")
	config.InitCacheIntervalSecondsFlag(flags)
	config.InitRedisIntervalSecondsFlag(flags)
	config.InitDBIntervalSecondsFlag(flags)
	return cmd
}

func runMetrics(cmd *cobra.Command, _ []string) error {
	config.LogFlagValues()

	ctx := cmd.Context()
	port := viper.GetInt(config.FlagMetricsPort)
	cacheInterval := time.Duration(viper.GetInt(config.FlagCacheIntervalSeconds)) * time.Second
	redisInterval := time.Duration(viper.GetInt(config.FlagRedisIntervalSeconds)) * time.Second
	dbInterval := time.Duration(viper.GetInt(config.FlagDBIntervalSeconds)) * time.Second

	log.Info().
		Int("port", port).
		Dur("cache_interval_ms", cacheInterval).
		Dur("redis_interval_ms", redisInterval).
		Dur("db_interval_ms", dbInterval).
		Msg("Starting metrics server")

	metrics.SetBuildInfo(config.GetBuildNumberAsInt())

	// Start collectors with independent intervals
	go runCacheCollector(ctx, cacheInterval)
	go runRedisCollector(ctx, redisInterval)
	go runDatabaseCollector(ctx, dbInterval)

	// Start metrics server (blocks)
	addr := fmt.Sprintf(":%d", port)
	metrics.StartServer(addr)
	return nil
}

// runCacheCollector periodically collects cache file metrics
func runCacheCollector(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("Starting cache collector")

	// Collect immediately on startup
	if err := computeAndUpdateCacheMetrics(ctx); err != nil {
		log.Error().Err(err).Msg("Initial cache metrics computation failed")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := computeAndUpdateCacheMetrics(ctx); err != nil {
				log.Error().Err(err).Msg("Cache metrics computation failed")
			}
		}
	}
}

// runRedisCollector periodically checks Redis for OAuth token existence
func runRedisCollector(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("Starting Redis collector")

	redisClient := redis.NewClient()
	defer redisClient.Close()

	// Collect immediately on startup
	collectRedisMetrics(ctx, redisClient)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectRedisMetrics(ctx, redisClient)
		}
	}
}

// runDatabaseCollector periodically collects database table row counts
func runDatabaseCollector(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("Starting database collector")

	db, err := database.OpenGorm()
	if err != nil {
		log.Error().Err(err).Msg("Failed to open database for metrics collector")
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get sql.DB for metrics collector")
		return
	}
	defer sqlDB.Close()

	// Collect immediately on startup
	collectDatabaseMetrics(db)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectDatabaseMetrics(db)
		}
	}
}

func collectRedisMetrics(ctx context.Context, redisClient redis.Client) {
	start := time.Now()
	hasToken, err := redis.HasValidToken(ctx, redisClient, config.DefaultUser)
	if err != nil {
		log.Error().Err(err).Str("user", config.DefaultUser).Msg("Failed to check OAuth token")
		return
	}
	metrics.SetRedisOAuthTokenValid(config.DefaultUser, hasToken)
	metrics.SetRedisMetricsTimestamp()
	log.Info().
		Str("user", config.DefaultUser).
		Bool("has_token", hasToken).
		Dur("duration", time.Since(start)).
		Msg("Redis metrics updated")
}

func collectDatabaseMetrics(db *gorm.DB) {
	start := time.Now()

	tables := []string{"players", "nhl_teams", "nhl_divisions", "nhl_conferences", "games", "leagues", "teams"}
	var totalRows int64
	for _, table := range tables {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			log.Warn().Err(err).Str("table", table).Msg("Failed to count rows")
			continue
		}
		metrics.SetDBTableRowCount(table, count)
		totalRows += count
	}

	// Query database size
	var dbSizeBytes int64
	if err := db.Raw("SELECT pg_database_size(current_database())").Scan(&dbSizeBytes).Error; err != nil {
		log.Warn().Err(err).Msg("Failed to get database size")
	} else {
		metrics.SetDBSizeBytes(dbSizeBytes)
	}

	metrics.SetDBMetricsTimestamp()
	log.Info().
		Int("tables", len(tables)).
		Int64("total_rows", totalRows).
		Int64("db_size_bytes", dbSizeBytes).
		Dur("duration", time.Since(start)).
		Msg("Database metrics updated")
}

func computeAndUpdateCacheMetrics(ctx context.Context) error {
	start := time.Now()

	redisClient := redis.NewClient()
	defer redisClient.Close()

	cacheData, err := getAllMetrics(ctx, redisClient)
	if err != nil {
		return err
	}

	// Update Prometheus gauges per season and file type
	for _, s := range cacheData {
		season := fmt.Sprintf("%d", s.seasonYear)
		metrics.SetCacheMetrics(season, s.fileType, s.expected, s.found)
	}

	// Compute and set totals
	var totalExpected, totalFound int
	for _, s := range cacheData {
		totalExpected += s.expected
		totalFound += s.found
	}
	metrics.SetCacheMetrics("total", "all", totalExpected, totalFound)

	// Calculate disk usage and file type stats
	dataPath := viper.GetString(config.FlagDataPath)
	if dataPath != "" {
		diskSize, err := calculateDirSize(dataPath)
		if err != nil {
			log.Warn().Err(err).Str("path", dataPath).Msg("Failed to calculate cache disk size")
		} else {
			metrics.SetCacheDiskSizeBytes(diskSize)
		}

		// Collect file stats by type
		fileStats := collectFileTypeStats(dataPath)
		for fileType, stats := range fileStats {
			metrics.SetDataPathFileStats(fileType, stats.count, stats.bytes)
		}
	}

	metrics.SetCacheMetricsTimestamp()
	metrics.SetCacheComputeDuration(time.Since(start))

	seasonCount := countUniqueSeasons(cacheData)
	log.Info().
		Int("seasons", seasonCount).
		Int("total_expected", totalExpected).
		Int("total_found", totalFound).
		Dur("duration", time.Since(start)).
		Msg("Cache metrics updated")

	return nil
}

// calculateDirSize calculates the total size of a directory, excluding .git
func calculateDirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip .git directory
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// fileTypeStats holds count and size for a file type
type fileTypeStats struct {
	count int64
	bytes int64
}

// collectFileTypeStats walks the data path and categorizes files by type
func collectFileTypeStats(dataPath string) map[string]*fileTypeStats {
	stats := make(map[string]*fileTypeStats)

	_ = filepath.WalkDir(dataPath, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		// Skip .git directory
		if strings.Contains(filePath, "/.git/") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		fileType := classifyFileType(dataPath, filePath, d.Name())
		if stats[fileType] == nil {
			stats[fileType] = &fileTypeStats{}
		}
		stats[fileType].count++
		stats[fileType].bytes += info.Size()

		return nil
	})

	return stats
}

// classifyFileType determines the file type based on path and filename
func classifyFileType(dataPath, filePath, filename string) string {
	relPath, _ := filepath.Rel(dataPath, filePath)
	parts := strings.Split(relPath, string(filepath.Separator))

	if len(parts) == 0 {
		return cache.FileTypeUnknown
	}

	// Check top-level directories first
	switch parts[0] {
	case "players":
		return cache.FileTypePlayerLanding
	case "yahoo-players":
		return cache.FileTypeYahooPlayer
	case "yahoo-players-missing":
		return cache.FileTypeYahooPlayer
	case "game-keys":
		return cache.FileTypeGameKey
	}

	// Check filename patterns for games directory
	if strings.HasPrefix(filename, "boxscore-") {
		return cache.FileTypeBoxscore
	}
	if strings.HasPrefix(filename, "daily-schedule-") {
		return cache.FileTypeDailySchedule
	}

	// Check for league/team/roster/summary in nested directories
	if strings.HasPrefix(filename, "league-") {
		return cache.FileTypeLeague
	}
	if strings.HasPrefix(filename, "team-") {
		return cache.FileTypeTeam
	}
	if strings.HasPrefix(filename, "rosters-") {
		return cache.FileTypeRoster
	}
	if strings.HasPrefix(filename, "team-summary-") {
		return cache.FileTypeTeamSummary
	}

	return cache.FileTypeUnknown
}

func countUniqueSeasons(cacheData []cacheMetrics) int {
	seen := make(map[int]bool)
	for _, s := range cacheData {
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
func getAllMetrics(ctx context.Context, redisClient redis.Client) ([]cacheMetrics, error) {
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
			fs := cache.NewSimpleCache()

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

// checkNHLSeasonCache checks NHL API files (daily schedule, boxscores)
func checkNHLSeasonCache(ctx context.Context, fs cache.FileSystem, redisClient redis.Client, season simpleSeason) []cacheMetrics {
	cacheData := make([]cacheMetrics, 0)
	seasonYear := season.StartYear()
	daysInSeason := countDays(season.start, season.end)

	// Count daily schedule files (1 per day)
	dailyScheduleCount := countDailyScheduleFilesSimple(fs, season)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   cache.FileTypeDailySchedule,
		expected:   daysInSeason,
		found:      dailyScheduleCount,
	})

	// Count boxscore files (depends on daily-schedule files)
	expectedBoxscores, foundBoxscores := countBoxscoreFilesSimple(ctx, fs, redisClient, season)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   cache.FileTypeBoxscore,
		expected:   expectedBoxscores,
		found:      foundBoxscores,
	})

	return cacheData
}

// checkYahooSeasonCache checks Yahoo fantasy files (leagues, teams, rosters, summaries)
func checkYahooSeasonCache(fs cache.FileSystem, nhlSeason simpleSeason, yahooCfg config.Season) []cacheMetrics {
	cacheData := make([]cacheMetrics, 0)
	seasonYear := nhlSeason.startYear
	daysInSeason := countDays(nhlSeason.start, nhlSeason.end)

	// Count leagues
	leagueCount := countLeagueFiles(fs, seasonYear, yahooCfg)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   cache.FileTypeLeague,
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
		fileType:   cache.FileTypeTeam,
		expected:   totalTeams,
		found:      teamCount,
	})

	// Count rosters (1 per team per day)
	expectedRosters := totalTeams * daysInSeason
	rosterCount := countRosterFiles(fs, nhlSeason, yahooCfg)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   cache.FileTypeRoster,
		expected:   expectedRosters,
		found:      rosterCount,
	})

	// Count team summaries (1 per team per day)
	expectedSummaries := totalTeams * daysInSeason
	summaryCount := countTeamSummaryFiles(fs, nhlSeason, yahooCfg)
	cacheData = append(cacheData, cacheMetrics{
		seasonYear: seasonYear,
		fileType:   cache.FileTypeTeamSummary,
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
				file := cache.RosterFile{Date: current, LeagueID: league.LeagueID, TeamID: teamID}
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
				file := cache.TeamSummaryFile{Date: current, LeagueID: league.LeagueID, TeamID: teamID}
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
		file := cache.DailyScheduleFile{Date: current}
		if fs.Exists(file) {
			count++
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

func countBoxscoreFilesSimple(ctx context.Context, fs cache.FileSystem, redisClient redis.Client, season simpleSeason) (expected int, found int) {
	current := season.start
	end := season.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		dailyScheduleFile := cache.DailyScheduleFile{Date: current}
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

		expected += len(gameIDs)

		for _, gameID := range gameIDs {
			boxscoreFile := cache.BoxscoreFile{Date: current, GameID: gameID}
			if fs.Exists(boxscoreFile) {
				found++
			}
		}

		current = current.AddDate(0, 0, 1)
	}

	return expected, found
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
