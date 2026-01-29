package cmd

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/redis"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// spinner displays an animated spinner with a message (supports multi-line)
type spinner struct {
	frames    []string
	message   string
	writer    io.Writer
	interval  time.Duration
	stop      chan struct{}
	done      chan struct{}
	mu        sync.Mutex
	once      sync.Once
	lineCount int // tracks number of lines in current message
}

const spinnerInterval = 125 * time.Millisecond

func newSpinner(w io.Writer, message string) *spinner {
	return &spinner{
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		message:  message,
		writer:   w,
		interval: spinnerInterval,
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
				// Clear all lines
				s.mu.Lock()
				s.clearLines()
				s.mu.Unlock()
				return
			default:
				s.mu.Lock()
				// Move cursor up and clear previous lines if multi-line
				s.clearLines()
				// Count lines in new message
				s.lineCount = strings.Count(s.message, "\n") + 1
				// Print lines with spinner on the last line
				lines := strings.Split(s.message, "\n")
				for j := 0; j < len(lines)-1; j++ {
					fmt.Fprintf(s.writer, "  %s\n", lines[j])
				}
				fmt.Fprintf(s.writer, "%s %s", s.frames[i], lines[len(lines)-1])
				s.mu.Unlock()
				i = (i + 1) % len(s.frames)
				time.Sleep(s.interval)
			}
		}
	}()
}

func (s *spinner) clearLines() {
	if s.lineCount > 1 {
		// Move cursor up to first line
		fmt.Fprintf(s.writer, "\033[%dA", s.lineCount-1)
	}
	// Clear from cursor to end of screen
	fmt.Fprintf(s.writer, "\r\033[J")
}

func (s *spinner) Stop() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
	})
}

// PrintAbove clears the spinner, runs the given function to print output,
// then continues rendering below that output. The printed content becomes permanent.
func (s *spinner) PrintAbove(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLines()
	s.lineCount = 0
	fn()
}

func cmdCheck() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "check",
		Short: "Verify system status",
		Long:  `Verify system status (cache, database)`,
	}
	cmd.AddCommand(cmdCheckCache())
	// cmd.AddCommand(cmdCheckDB())      // commented out: uses old Season struct
	return cmd
}

const (
	FlagVerbose    = "verbose"
	FlagIncomplete = "incomplete"
)

func cmdCheckCache() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "cache",
		Short: "Verify cache completeness",
		Long: `Calculate expected vs actual cache files for NHL and Yahoo data.
Use --season to check a specific season, or --from-season/--to-season for a range.
If --seasons config file is provided, Yahoo files are also checked.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
				return err
			}
			if err := viper.BindPFlag(FlagVerbose, flags.Lookup(FlagVerbose)); err != nil {
				return err
			}
			if err := viper.BindPFlag(FlagIncomplete, flags.Lookup(FlagIncomplete)); err != nil {
				return err
			}
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			return config.BindSeasonRangeFlags(flags)
		},
		RunE: runCheckCache,
	}
	flags := cmd.Flags()
	config.InitDataPathFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitRedisFlags(flags)
	flags.StringP(config.FlagYahooSeasons, "S", "yahoo-seasons.yaml", "Yahoo seasons config file (optional, enables Yahoo file checks)")
	flags.BoolP(FlagVerbose, "v", false, "Show detailed output per season")
	flags.BoolP(FlagIncomplete, "i", false, "Only show seasons with less than 100% completion")
	return cmd
}

// cacheMetrics, seasonResult, simpleSeason, and related functions are defined in metrics.go

func runCheckCache(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	redisClient := redis.NewClient()
	defer redisClient.Close()

	sp := newSpinner(cmd.OutOrStdout(), "Counting cache files...")
	sp.Start()

	allMetrics, err := getAllMetrics(cmd.Context(), redisClient)
	sp.Stop()

	if err != nil {
		return err
	}

	incompleteOnly := viper.GetBool(FlagIncomplete)
	if viper.GetBool(FlagVerbose) {
		printCacheMetricsVerbose(allMetrics, incompleteOnly)
	} else {
		printCacheMetricsCompact(allMetrics, incompleteOnly)
	}
	return nil
}

// countDays, countLeagueFiles, countTeamFiles, countRosterFiles,
// countTeamSummaryFiles, countDailyScheduleFilesSimple, countBoxscoreFilesSimple
// are defined in metrics.go

func printCacheMetricsVerbose(cacheData []cacheMetrics, incompleteOnly bool) {
	// Group by season
	seasonMap := make(map[int][]cacheMetrics)
	for _, s := range cacheData {
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

		// Calculate season totals first to check percentage
		seasonExpected := 0
		seasonFound := 0
		for _, s := range seasonStats {
			seasonExpected += s.expected
			seasonFound += s.found
		}

		pct := float64(0)
		if seasonExpected > 0 {
			pct = float64(seasonFound) / float64(seasonExpected) * 100
		}

		// Skip complete seasons if incompleteOnly is set
		if incompleteOnly && pct >= 100.0 {
			totalExpected += seasonExpected
			totalFound += seasonFound
			continue
		}

		fmt.Printf("Season %d-%d:\n", year, year+1)
		fmt.Println("  ┌───────────────┬──────────┬───────┬─────────┐")
		fmt.Println("  │ Type          │ Expected │ Found │ Percent │")
		fmt.Println("  ├───────────────┼──────────┼───────┼─────────┤")

		for _, s := range seasonStats {
			fmt.Printf("  │ %-13s │ %8s │ %5s │ %6.1f%% │\n",
				cache.FileTypeName(s.fileType),
				strconv.Itoa(s.expected),
				strconv.Itoa(s.found),
				s.percentage())
		}

		fmt.Println("  ├───────────────┼──────────┼───────┼─────────┤")
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

// seasonFileStats holds expected/found counts for each file type in a season
type seasonFileStats struct {
	year        int
	dailySched  [2]int // [expected, found]
	boxscore    [2]int
	league      [2]int // Yahoo - may be empty
	team        [2]int // Yahoo - may be empty
	roster      [2]int // Yahoo - may be empty
	teamSummary [2]int // Yahoo - may be empty
	hasYahoo    bool
}

func (s seasonFileStats) totalExpected() int {
	total := s.dailySched[0] + s.boxscore[0]
	if s.hasYahoo {
		total += s.league[0] + s.team[0] + s.roster[0] + s.teamSummary[0]
	}
	return total
}

func (s seasonFileStats) totalFound() int {
	total := s.dailySched[1] + s.boxscore[1]
	if s.hasYahoo {
		total += s.league[1] + s.team[1] + s.roster[1] + s.teamSummary[1]
	}
	return total
}

func (s seasonFileStats) percentage() float64 {
	exp := s.totalExpected()
	if exp == 0 {
		return 0
	}
	return float64(s.totalFound()) / float64(exp) * 100
}

// column indices for the compact table
const (
	colSeason = iota
	colDailySched
	colBoxscore
	colLeague
	colTeam
	colRoster
	colSummary
	colTotal
	colPercent
	numColumns
)

var columnHeaders = [numColumns]string{
	"Season", "DailySch", "Boxscore", "League", "Team", "Roster", "Summary", "Total", "%",
}

func printCacheMetricsCompact(cacheData []cacheMetrics, incompleteOnly bool) {
	// Build seasonFileStats for each season
	seasonMap := make(map[int]*seasonFileStats)
	for _, s := range cacheData {
		sf, ok := seasonMap[s.seasonYear]
		if !ok {
			sf = &seasonFileStats{year: s.seasonYear}
			seasonMap[s.seasonYear] = sf
		}

		switch s.fileType {
		case cache.DailyScheduleFileType:
			sf.dailySched = [2]int{s.expected, s.found}
		case cache.BoxscoreFileType:
			sf.boxscore = [2]int{s.expected, s.found}
		case cache.LeagueFileType:
			sf.league = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case cache.TeamFileType:
			sf.team = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case cache.RosterFileType:
			sf.roster = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case cache.TeamSummaryFileType:
			sf.teamSummary = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		}
	}

	// Sort seasons
	years := make([]int, 0, len(seasonMap))
	for year := range seasonMap {
		years = append(years, year)
	}
	sort.Ints(years)

	// Check if any season has Yahoo data
	hasAnyYahoo := false
	for _, sf := range seasonMap {
		if sf.hasYahoo {
			hasAnyYahoo = true
			break
		}
	}

	// Calculate totals
	var totDS, totBx, totLg, totTm, totRs, totTS [2]int
	for _, sf := range seasonMap {
		totDS[0] += sf.dailySched[0]
		totDS[1] += sf.dailySched[1]
		totBx[0] += sf.boxscore[0]
		totBx[1] += sf.boxscore[1]
		if sf.hasYahoo {
			totLg[0] += sf.league[0]
			totLg[1] += sf.league[1]
			totTm[0] += sf.team[0]
			totTm[1] += sf.team[1]
			totRs[0] += sf.roster[0]
			totRs[1] += sf.roster[1]
			totTS[0] += sf.teamSummary[0]
			totTS[1] += sf.teamSummary[1]
		}
	}

	grandTotalExp := totDS[0] + totBx[0] + totLg[0] + totTm[0] + totRs[0] + totTS[0]
	grandTotalFound := totDS[1] + totBx[1] + totLg[1] + totTm[1] + totRs[1] + totTS[1]
	grandPct := float64(0)
	if grandTotalExp > 0 {
		grandPct = float64(grandTotalFound) / float64(grandTotalExp) * 100
	}

	// Build all row data first to calculate column widths
	type rowData struct {
		cols [numColumns]string
	}
	var rows []rowData

	for _, year := range years {
		sf := seasonMap[year]

		// Skip complete seasons if incompleteOnly is set
		if incompleteOnly && sf.percentage() >= 100.0 {
			continue
		}

		var row rowData
		row.cols[colSeason] = fmt.Sprintf("%d-%02d", year, (year+1)%100)
		row.cols[colDailySched] = formatCountPair(sf.dailySched[1], sf.dailySched[0])
		row.cols[colBoxscore] = formatCountPair(sf.boxscore[1], sf.boxscore[0])

		if sf.hasYahoo {
			row.cols[colLeague] = formatCountPair(sf.league[1], sf.league[0])
			row.cols[colTeam] = formatCountPair(sf.team[1], sf.team[0])
			row.cols[colRoster] = formatCountPair(sf.roster[1], sf.roster[0])
			row.cols[colSummary] = formatCountPair(sf.teamSummary[1], sf.teamSummary[0])
		}

		row.cols[colTotal] = formatCountPair(sf.totalFound(), sf.totalExpected())
		row.cols[colPercent] = fmt.Sprintf("%.1f%%", sf.percentage())
		rows = append(rows, row)
	}

	// Build totals row
	var totalsRow rowData
	totalsRow.cols[colSeason] = "TOTAL"
	totalsRow.cols[colDailySched] = formatCountPair(totDS[1], totDS[0])
	totalsRow.cols[colBoxscore] = formatCountPair(totBx[1], totBx[0])
	if hasAnyYahoo {
		totalsRow.cols[colLeague] = formatCountPair(totLg[1], totLg[0])
		totalsRow.cols[colTeam] = formatCountPair(totTm[1], totTm[0])
		totalsRow.cols[colRoster] = formatCountPair(totRs[1], totRs[0])
		totalsRow.cols[colSummary] = formatCountPair(totTS[1], totTS[0])
	}
	totalsRow.cols[colTotal] = formatCountPair(grandTotalFound, grandTotalExp)
	totalsRow.cols[colPercent] = fmt.Sprintf("%.1f%%", grandPct)

	// Calculate column widths (max of header and all data)
	widths := [numColumns]int{}
	for i := 0; i < numColumns; i++ {
		widths[i] = len(columnHeaders[i])
	}
	for _, row := range rows {
		for i, col := range row.cols {
			if len(col) > widths[i] {
				widths[i] = len(col)
			}
		}
	}
	for i, col := range totalsRow.cols {
		if len(col) > widths[i] {
			widths[i] = len(col)
		}
	}

	// Determine which columns to show
	showCol := [numColumns]bool{true, true, true, hasAnyYahoo, hasAnyYahoo, hasAnyYahoo, hasAnyYahoo, true, true}

	// Print report
	fmt.Println()
	fmt.Println("Cache Status Report")
	fmt.Println("===================")
	fmt.Println()

	// Print header
	printTableRow(columnHeaders[:], widths[:], showCol[:], true)
	printTableSeparator(widths[:], showCol[:])

	// Print data rows
	for _, row := range rows {
		printTableRow(row.cols[:], widths[:], showCol[:], false)
	}

	// Print separator and totals
	printTableSeparator(widths[:], showCol[:])
	printTableRow(totalsRow.cols[:], widths[:], showCol[:], false)

	fmt.Println()
}

func printTableRow(cols []string, widths []int, showCol []bool, isHeader bool) {
	first := true
	for i, col := range cols {
		if !showCol[i] {
			continue
		}
		if !first {
			fmt.Print(" │ ")
		}
		first = false
		if isHeader || i == colSeason {
			fmt.Printf("%-*s", widths[i], col)
		} else {
			fmt.Printf("%*s", widths[i], col)
		}
	}
	fmt.Println()
}

func printTableSeparator(widths []int, showCol []bool) {
	first := true
	for i, w := range widths {
		if !showCol[i] {
			continue
		}
		if !first {
			fmt.Print("─┼─")
		}
		first = false
		fmt.Print(strings.Repeat("─", w))
	}
	fmt.Println()
}

func formatCountPair(found, expected int) string {
	if expected == 0 && found == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d", found, expected)
}

/*
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
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
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
*/

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

/*
func runCheckDB(cmd *cobra.Command, args []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	seasons, err := config.GetYahooSeasonsConfig()
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
	daysInSeason := countDays(season.Start, season.End)

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
*/

/*
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
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
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

	seasons, err := config.GetYahooSeasonsConfig()
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
*/

/*
func checkSeasonParsing(fs cache.FileSystem, season config.Season, deleteOnFail bool) (checked, failed, deleted int, failures []string) {
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
*/

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
