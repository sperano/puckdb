package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/cache"
	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/database"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// spinner displays an animated spinner with a message
type spinner struct {
	frames   []string
	message  string
	writer   io.Writer
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	once     sync.Once
}

func newSpinner(w io.Writer, message string) *spinner {
	return &spinner{
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		message:  message,
		writer:   w,
		interval: 80 * time.Millisecond,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (s *spinner) Start() {
	go func() {
		defer close(s.done)
		i := 0
		for {
			select {
			case <-s.stop:
				// Clear the spinner line
				fmt.Fprintf(s.writer, "\r\033[K")
				return
			default:
				s.mu.Lock()
				fmt.Fprintf(s.writer, "\r\033[K%s %s", s.frames[i], s.message)
				s.mu.Unlock()
				i = (i + 1) % len(s.frames)
				time.Sleep(s.interval)
			}
		}
	}()
}

func (s *spinner) Stop() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
	})
}

func cmdCheck() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "check",
		Short: "Check various aspects of the system",
		Long:  `Check various aspects of the system such as cache status`,
	}
	cmd.AddCommand(cmdCheckCache())
	cmd.AddCommand(cmdCheckDB())
	cmd.AddCommand(cmdCheckParsing())
	return cmd
}

func cmdCheckCache() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "cache",
		Short: "Check cache file status",
		Long:  `Calculate expected vs actual files in the cache for all seasons`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(config.FlagSeasons, flags.Lookup(config.FlagSeasons)); err != nil {
				return err
			}
			return viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath))
		},
		RunE: runCheckCache,
	}
	flags := cmd.Flags()
	config.InitSeasonsFlag(cmd, flags, false)
	config.InitDataPathFlag(flags)
	return cmd
}

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

func getAllStats() ([]cacheStats, error) {
	seasons, err := config.GetSeasonsConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load seasons config: %w", err)
	}

	if viper.GetString(config.FlagDataPath) == "" {
		return nil, fmt.Errorf("data-path is required")
	}

	results := make(chan seasonResult, len(seasons))
	var wg sync.WaitGroup

	for _, season := range seasons {
		wg.Add(1)
		go func(s config.Season) {
			defer wg.Done()
			fs := cache.NewSimpleCache()
			stats, err := checkSeasonCache(fs, s)
			results <- seasonResult{stats: stats, err: err, year: s.StartYear()}
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

func runCheckCache(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	sp := newSpinner(cmd.OutOrStdout(), "Counting cache files...")
	sp.Start()

	allStats, err := getAllStats()
	sp.Stop()

	if err != nil {
		return err
	}
	printCacheStats(allStats)
	return nil
}

func checkSeasonCache(fs *cache.SimpleFS, season config.Season) ([]cacheStats, error) {
	stats := make([]cacheStats, 0)
	seasonYear := season.StartYear()
	daysInSeason := countDaysInSeason(season)

	// Count leagues
	leagueStats := countLeagueFiles(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.LeagueFileType,
		expected:   len(season.Leagues),
		found:      leagueStats,
	})

	// Count teams
	totalTeams := 0
	for _, league := range season.Leagues {
		totalTeams += len(league.TeamIDs)
	}
	teamStats := countTeamFiles(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.TeamFileType,
		expected:   totalTeams,
		found:      teamStats,
	})

	// Count rosters (1 per team per day)
	expectedRosters := totalTeams * daysInSeason
	rosterStats := countRosterFiles(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.RosterFileType,
		expected:   expectedRosters,
		found:      rosterStats,
	})

	// Count team summaries (1 per team per day)
	expectedSummaries := totalTeams * daysInSeason
	summaryStats := countTeamSummaryFiles(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.TeamSummaryFileType,
		expected:   expectedSummaries,
		found:      summaryStats,
	})

	// Count games list files (1 per day)
	gamesListStats := countGamesListFiles(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.GamesListFileType,
		expected:   daysInSeason,
		found:      gamesListStats,
	})

	// Count game files (depends on games-list files)
	expectedGames, foundGames := countGameFiles(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.GameFileType,
		expected:   expectedGames,
		found:      foundGames,
	})

	// Count daily schedule files (1 per day)
	dailyScheduleStats := countDailyScheduleFiles(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.DailyScheduleFileType,
		expected:   daysInSeason,
		found:      dailyScheduleStats,
	})

	// Count boxscore files (depends on daily-schedule files)
	expectedBoxscores, foundBoxscores := countBoxscoreFiles(fs, season)
	stats = append(stats, cacheStats{
		seasonYear: seasonYear,
		fileType:   cache.BoxscoreFileType,
		expected:   expectedBoxscores,
		found:      foundBoxscores,
	})

	return stats, nil
}

func countDaysInSeason(season config.Season) int {
	end := season.End
	// If end is in the future, use today
	if end.After(time.Now()) {
		end = time.Now()
	}
	return int(end.Sub(season.Start).Hours()/24) + 1
}

func countLeagueFiles(fs *cache.SimpleFS, season config.Season) int {
	count := 0
	for _, league := range season.Leagues {
		file := cache.LeagueFile{Season: season.StartYear(), LeagueID: league.LeagueID}
		if fs.Exists(file) {
			count++
		}
	}
	return count
}

func countTeamFiles(fs *cache.SimpleFS, season config.Season) int {
	count := 0
	for _, league := range season.Leagues {
		for _, teamID := range league.TeamIDs {
			file := cache.TeamFile{Season: season.StartYear(), LeagueID: league.LeagueID, TeamID: teamID}
			if fs.Exists(file) {
				count++
			}
		}
	}
	return count
}

func countRosterFiles(fs *cache.SimpleFS, season config.Season) int {
	count := 0
	current := season.Start
	end := season.End
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		for _, league := range season.Leagues {
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

func countTeamSummaryFiles(fs *cache.SimpleFS, season config.Season) int {
	count := 0
	current := season.Start
	end := season.End
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		for _, league := range season.Leagues {
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

func countGamesListFiles(fs *cache.SimpleFS, season config.Season) int {
	count := 0
	current := season.Start
	end := season.End
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		file := fs.New(cache.GamesListFileType, current)
		if fs.Exists(file) {
			count++
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

// countGameFiles counts expected vs found game files by parsing games-list files.
// Expected count comes from parsing each games-list file to count game links.
// Found count is the number of those game files that exist in the cache.
func countGameFiles(fs *cache.SimpleFS, season config.Season) (expected int, found int) {
	current := season.Start
	end := season.End
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		// Check if games-list file exists for this day
		gamesListFile := fs.New(cache.GamesListFileType, current)
		if !fs.Exists(gamesListFile) {
			// No games-list file for this day, skip
			current = current.AddDate(0, 0, 1)
			continue
		}

		// Parse the games-list file to get game links
		gameLinks, err := cache.ParseGamesList(fs, gamesListFile)
		if err != nil {
			log.Warn().Err(err).Time("date", current).Msg("Error parsing games-list file")
			current = current.AddDate(0, 0, 1)
			continue
		}

		// Count expected games from this day
		expected += len(gameLinks)

		// Check which game files exist
		for _, link := range gameLinks {
			gameFile := fs.New(cache.GameFileType, current, link)
			if fs.Exists(gameFile) {
				found++
			}
		}

		current = current.AddDate(0, 0, 1)
	}

	return expected, found
}

func countDailyScheduleFiles(fs *cache.SimpleFS, season config.Season) int {
	count := 0
	current := season.Start
	end := season.End
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

// countBoxscoreFiles counts expected vs found boxscore files by parsing daily-schedule files.
// Expected count comes from parsing each daily-schedule file to count game IDs.
// Found count is the number of those boxscore files that exist in the cache.
func countBoxscoreFiles(fs *cache.SimpleFS, season config.Season) (expected int, found int) {
	current := season.Start
	end := season.End
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		// Check if daily-schedule file exists for this day
		dailyScheduleFile := fs.New(cache.DailyScheduleFileType, current)
		if !fs.Exists(dailyScheduleFile) {
			// No daily-schedule file for this day, skip
			current = current.AddDate(0, 0, 1)
			continue
		}

		// Parse the daily-schedule file to get game IDs
		gameIDs, err := cache.ParseDailySchedule(fs, dailyScheduleFile)
		if err != nil {
			log.Warn().Err(err).Time("date", current).Msg("Error parsing daily-schedule file")
			current = current.AddDate(0, 0, 1)
			continue
		}

		// Count expected boxscores from this day
		expected += len(gameIDs)

		// Check which boxscore files exist
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

func countFilesInDir(dir string, prefix string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(prefix) > 0 && len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			count++
		}
	}
	return count
}

func printCacheStats(stats []cacheStats) {
	// Group by season
	seasonMap := make(map[int][]cacheStats)
	for _, s := range stats {
		seasonMap[s.seasonYear] = append(seasonMap[s.seasonYear], s)
	}

	// Get sorted season years
	years := make([]int, 0, len(seasonMap))
	for year := range seasonMap {
		years = append(years, year)
	}
	sort.Ints(years)

	// Print header
	fmt.Println()
	fmt.Println("Cache Status Report")
	fmt.Println("===================")
	fmt.Println()

	totalExpected := 0
	totalFound := 0

	for _, year := range years {
		seasonStats := seasonMap[year]
		fmt.Printf("Season %d-%d:\n", year, year+1)
		fmt.Println("  ┌───────────────┬──────────┬───────┬─────────┐")
		fmt.Println("  │ Type          │ Expected │ Found │ Percent │")
		fmt.Println("  ├───────────────┼──────────┼───────┼─────────┤")

		seasonExpected := 0
		seasonFound := 0

		for _, s := range seasonStats {
			fmt.Printf("  │ %-13s │ %8s │ %5s │ %6.1f%% │\n",
				cache.FileTypeName(s.fileType),
				strconv.Itoa(s.expected),
				strconv.Itoa(s.found),
				s.percentage())
			seasonExpected += s.expected
			seasonFound += s.found
		}

		fmt.Println("  ├───────────────┼──────────┼───────┼─────────┤")
		pct := float64(0)
		if seasonExpected > 0 {
			pct = float64(seasonFound) / float64(seasonExpected) * 100
		}
		fmt.Printf("  │ %-13s │ %8s │ %5s │ %6.1f%% │\n",
			"TOTAL",
			strconv.Itoa(seasonExpected),
			strconv.Itoa(seasonFound),
			pct)
		fmt.Println("  └───────────────┴──────────┴───────┴─────────┘")
		fmt.Println()

		totalExpected += seasonExpected
		totalFound += seasonFound
	}

	// Print grand total
	fmt.Println("Grand Total:")
	pct := float64(0)
	if totalExpected > 0 {
		pct = float64(totalFound) / float64(totalExpected) * 100
	}
	fmt.Printf("  Expected %s, found %s (%.1f%%)\n",
		english.Plural(totalExpected, "file", ""),
		english.Plural(totalFound, "file", ""),
		pct)
	fmt.Println()
}

// ////////////////////////////////////////////////////////////////////////////
// DB CHECK
// ////////////////////////////////////////////////////////////////////////////

func cmdCheckDB() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "db",
		Short: "Check database record status",
		Long:  `Calculate expected vs actual records in the database for all seasons`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(config.FlagSeasons, flags.Lookup(config.FlagSeasons)); err != nil {
				return err
			}
			return config.BindPostgresFlags(flags)
		},
		RunE: runCheckDB,
	}
	flags := cmd.Flags()
	config.InitSeasonsFlag(cmd, flags, false)
	config.InitPostgresFlags(flags)
	return cmd
}

type dbStats struct {
	seasonYear int
	recordType string
	expected   int
	found      int
}

func (s dbStats) percentage() float64 {
	if s.expected == 0 {
		return 0
	}
	return float64(s.found) / float64(s.expected) * 100
}

func runCheckDB(cmd *cobra.Command, args []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	seasons, err := config.GetSeasonsConfig()
	if err != nil {
		return fmt.Errorf("failed to load seasons config: %w", err)
	}

	db, err := database.OpenGorm()
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	allStats := make([]dbStats, 0)

	for _, season := range seasons {
		seasonStats, err := checkSeasonDB(db, season)
		if err != nil {
			log.Warn().Err(err).Int("season", season.StartYear()).Msg("Error checking season")
			continue
		}
		allStats = append(allStats, seasonStats...)
	}

	printDBStats(allStats)
	return nil
}

func checkSeasonDB(db *gorm.DB, season config.Season) ([]dbStats, error) {
	stats := make([]dbStats, 0)
	seasonYear := season.StartYear()
	daysInSeason := countDaysInSeason(season)

	// Count leagues
	expectedLeagues := len(season.Leagues)
	foundLeagues := countLeaguesInDB(db, season)
	stats = append(stats, dbStats{
		seasonYear: seasonYear,
		recordType: "League",
		expected:   expectedLeagues,
		found:      foundLeagues,
	})

	// Count teams
	totalTeams := 0
	for _, league := range season.Leagues {
		totalTeams += len(league.TeamIDs)
	}
	foundTeams := countTeamsInDB(db, season)
	stats = append(stats, dbStats{
		seasonYear: seasonYear,
		recordType: "Team",
		expected:   totalTeams,
		found:      foundTeams,
	})

	// Count roster players (1 record per team per day per player, but we count unique team-date combos)
	// Expected: teams * days (each team should have roster entries for each day)
	expectedRosterDays := totalTeams * daysInSeason
	foundRosterDays := countRosterDaysInDB(db, season)
	stats = append(stats, dbStats{
		seasonYear: seasonYear,
		recordType: "RosterDays",
		expected:   expectedRosterDays,
		found:      foundRosterDays,
	})

	// Count team summaries (1 per team per day)
	expectedSummaries := totalTeams * daysInSeason
	foundSummaries := countTeamSummariesInDB(db, season)
	stats = append(stats, dbStats{
		seasonYear: seasonYear,
		recordType: "TeamSummary",
		expected:   expectedSummaries,
		found:      foundSummaries,
	})

	return stats, nil
}

func countLeaguesInDB(db *gorm.DB, season config.Season) int {
	var count int64
	leagueIDs := make([]int, len(season.Leagues))
	for i, l := range season.Leagues {
		leagueIDs[i] = l.LeagueID
	}
	db.Model(&database.League{}).
		Where("season = ? AND id IN ?", season.StartYear(), leagueIDs).
		Count(&count)
	return int(count)
}

func countTeamsInDB(db *gorm.DB, season config.Season) int {
	// Teams don't have a direct season field, so we count teams that match
	// the expected team IDs from the config
	var count int64
	teamIDs := make([]int, 0)
	for _, league := range season.Leagues {
		teamIDs = append(teamIDs, league.TeamIDs...)
	}
	db.Model(&database.Team{}).
		Where("id IN ?", teamIDs).
		Count(&count)
	return int(count)
}

func countRosterDaysInDB(db *gorm.DB, season config.Season) int {
	// Count unique (team_id, date) combinations within the season date range
	var count int64
	teamIDs := make([]int, 0)
	for _, league := range season.Leagues {
		teamIDs = append(teamIDs, league.TeamIDs...)
	}

	end := season.End
	if end.After(time.Now()) {
		end = time.Now()
	}

	db.Model(&database.RosterPlayer{}).
		Select("COUNT(DISTINCT (team_id, date))").
		Where("team_id IN ? AND date >= ? AND date <= ?", teamIDs, season.Start, end).
		Count(&count)
	return int(count)
}

func countTeamSummariesInDB(db *gorm.DB, season config.Season) int {
	var count int64
	teamIDs := make([]int, 0)
	for _, league := range season.Leagues {
		teamIDs = append(teamIDs, league.TeamIDs...)
	}

	end := season.End
	if end.After(time.Now()) {
		end = time.Now()
	}

	db.Model(&database.TeamSummary{}).
		Where("team_id IN ? AND date >= ? AND date <= ?", teamIDs, season.Start, end).
		Count(&count)
	return int(count)
}

// ////////////////////////////////////////////////////////////////////////////
// PARSING CHECK
// ////////////////////////////////////////////////////////////////////////////

const FlagDelete = "delete"

func cmdCheckParsing() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "parsing",
		Short: "Check game HTML parsing",
		Long:  `Attempt to parse all game HTML files and report any failures`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(config.FlagSeasons, flags.Lookup(config.FlagSeasons)); err != nil {
				return err
			}
			if err := viper.BindPFlag(FlagDelete, flags.Lookup(FlagDelete)); err != nil {
				return err
			}
			return viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath))
		},
		RunE: runCheckParsing,
	}
	flags := cmd.Flags()
	config.InitSeasonsFlag(cmd, flags, false)
	config.InitDataPathFlag(flags)
	flags.Bool(FlagDelete, false, "Delete files that fail to parse")
	return cmd
}

type seasonParseResult struct {
	checked  int
	failed   int
	deleted  int
	failures []string
}

func runCheckParsing(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	seasons, err := config.GetSeasonsConfig()
	if err != nil {
		return fmt.Errorf("failed to load seasons config: %w", err)
	}

	if viper.GetString(config.FlagDataPath) == "" {
		return fmt.Errorf("data-path is required")
	}

	deleteOnFail := viper.GetBool(FlagDelete)

	sp := newSpinner(cmd.OutOrStdout(), "Checking game HTML parsing...")
	sp.Start()

	// Process seasons in parallel
	results := make(chan seasonParseResult, len(seasons))
	var wg sync.WaitGroup

	for _, season := range seasons {
		wg.Add(1)
		go func(s config.Season) {
			defer wg.Done()
			fs := cache.NewSimpleCache()
			checked, failed, deleted, failures := checkSeasonParsing(fs, s, deleteOnFail)
			results <- seasonParseResult{
				checked:  checked,
				failed:   failed,
				deleted:  deleted,
				failures: failures,
			}
		}(season)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var totalChecked, totalFailed, totalDeleted int
	var failedFiles []string

	for result := range results {
		totalChecked += result.checked
		totalFailed += result.failed
		totalDeleted += result.deleted
		failedFiles = append(failedFiles, result.failures...)
	}

	sp.Stop()

	// Print results
	fmt.Println()
	fmt.Printf("Parsing Check Complete\n")
	fmt.Printf("======================\n")
	fmt.Printf("Files checked: %d\n", totalChecked)
	fmt.Printf("Files failed:  %d\n", totalFailed)
	if deleteOnFail {
		fmt.Printf("Files deleted: %d\n", totalDeleted)
	}
	fmt.Println()

	if len(failedFiles) > 0 {
		fmt.Println("Failed files:")
		for _, f := range failedFiles {
			fmt.Printf("  %s\n", f)
		}
		fmt.Println()
	}

	return nil
}

// gameFilePattern matches game HTML files but not games-list files
// Game files look like: team-team-YYYYMMDDXX.html
var gameFilePattern = regexp.MustCompile(`^[a-z]+-[a-z]+-.*\.html$`)

const numParsingWorkers = 16

type parseResult struct {
	filePath string
	failed   bool
	deleted  bool
}

func checkSeasonParsing(fs *cache.SimpleFS, season config.Season, deleteOnFail bool) (checked, failed, deleted int, failures []string) {
	// Walk the games directory for this season to collect all file paths
	gamesDir := filepath.Join(fs.RootPath, fmt.Sprintf("%d/games", season.StartYear()))

	var filePaths []string
	err := filepath.Walk(gamesDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		if info.IsDir() {
			return nil
		}

		// Only process HTML files that match game file pattern (not games-list files)
		fileName := filepath.Base(filePath)
		if !gameFilePattern.MatchString(fileName) {
			return nil
		}

		filePaths = append(filePaths, filePath)
		return nil
	})

	if err != nil {
		log.Warn().Err(err).Int("season", season.StartYear()).Msg("Error walking games directory")
	}

	checked = len(filePaths)
	if checked == 0 {
		return
	}

	// Process files in parallel
	jobs := make(chan string, len(filePaths))
	results := make(chan parseResult, len(filePaths))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numParsingWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for filePath := range jobs {
				result := parseResult{filePath: filePath}

				gameFile := &simpleFile{path: filePath, rootPath: fs.RootPath}
				_, parseErr := cache.ParseGameHTML(fs, gameFile)
				if parseErr != nil {
					result.failed = true

					if deleteOnFail {
						if rmErr := os.Remove(filePath); rmErr == nil {
							result.deleted = true
						}
					}
				}
				results <- result
			}
		}()
	}

	// Send jobs
	for _, fp := range filePaths {
		jobs <- fp
	}
	close(jobs)

	// Wait for workers and close results
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	for result := range results {
		if result.failed {
			failed++
			failures = append(failures, result.filePath)
			if result.deleted {
				deleted++
			}
		}
	}

	return
}

// simpleFile implements cache.File for direct path access
type simpleFile struct {
	path     string
	rootPath string
}

func (f *simpleFile) Dir() string {
	relPath, _ := filepath.Rel(f.rootPath, f.path)
	return filepath.Dir(relPath)
}

func (f *simpleFile) Name() string {
	base := filepath.Base(f.path)
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}

func (f *simpleFile) Ext() string {
	return filepath.Ext(f.path)[1:] // Remove the leading dot
}

func printDBStats(stats []dbStats) {
	// Group by season
	seasonMap := make(map[int][]dbStats)
	for _, s := range stats {
		seasonMap[s.seasonYear] = append(seasonMap[s.seasonYear], s)
	}

	// Get sorted season years
	years := make([]int, 0, len(seasonMap))
	for year := range seasonMap {
		years = append(years, year)
	}
	sort.Ints(years)

	// Print header
	fmt.Println()
	fmt.Println("Database Status Report")
	fmt.Println("======================")
	fmt.Println()

	totalExpected := 0
	totalFound := 0

	for _, year := range years {
		seasonStats := seasonMap[year]
		fmt.Printf("Season %d-%d:\n", year, year+1)
		fmt.Println("  ┌───────────────┬──────────┬───────┬─────────┐")
		fmt.Println("  │ Type          │ Expected │ Found │ Percent │")
		fmt.Println("  ├───────────────┼──────────┼───────┼─────────┤")

		seasonExpected := 0
		seasonFound := 0

		for _, s := range seasonStats {
			fmt.Printf("  │ %-13s │ %8s │ %5s │ %6.1f%% │\n",
				s.recordType,
				strconv.Itoa(s.expected),
				strconv.Itoa(s.found),
				s.percentage())
			seasonExpected += s.expected
			seasonFound += s.found
		}

		fmt.Println("  ├───────────────┼──────────┼───────┼─────────┤")
		pct := float64(0)
		if seasonExpected > 0 {
			pct = float64(seasonFound) / float64(seasonExpected) * 100
		}
		fmt.Printf("  │ %-13s │ %8s │ %5s │ %6.1f%% │\n",
			"TOTAL",
			strconv.Itoa(seasonExpected),
			strconv.Itoa(seasonFound),
			pct)
		fmt.Println("  └───────────────┴──────────┴───────┴─────────┘")
		fmt.Println()

		totalExpected += seasonExpected
		totalFound += seasonFound
	}

	// Print grand total
	fmt.Println("Grand Total:")
	pct := float64(0)
	if totalExpected > 0 {
		pct = float64(totalFound) / float64(totalExpected) * 100
	}
	fmt.Printf("  Expected %s, found %s (%.1f%%)\n",
		english.Plural(totalExpected, "record", ""),
		english.Plural(totalFound, "record", ""),
		pct)
	fmt.Println()
}
