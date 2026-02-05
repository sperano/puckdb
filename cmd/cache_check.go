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

// SpinnerPlaceholder is replaced with the current spinner frame when rendering.
// Use this in the message to position the spinner on a specific line.
const SpinnerPlaceholder = "\x00"

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

func newSpinner(w io.Writer, message string) *spinner {
	return &spinner{
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		message:  message,
		writer:   w,
		interval: config.DefaultSpinnerInterval,
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

				// Replace placeholder with spinner frame, or put spinner on last line
				if strings.Contains(s.message, SpinnerPlaceholder) {
					// Placeholder mode: replace placeholder with spinner frame
					output := strings.Replace(s.message, SpinnerPlaceholder, s.frames[i], 1)
					fmt.Fprint(s.writer, output)
				} else {
					// Legacy mode: spinner on last line
					lines := strings.Split(s.message, "\n")
					for j := 0; j < len(lines)-1; j++ {
						fmt.Fprintf(s.writer, "%s\n", lines[j])
					}
					fmt.Fprintf(s.writer, "%s %s", s.frames[i], lines[len(lines)-1])
				}
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

func cmdCacheCheck() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache-check",
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
			if err := config.BindVerboseFlag(flags); err != nil {
				return err
			}
			if err := config.BindIncompleteFlag(flags); err != nil {
				return err
			}
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			return config.BindSeasonRangeFlags(flags)
		},
		RunE: runCacheCheck,
	}
	flags := cmd.Flags()
	config.InitDataPathFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitRedisFlags(flags)
	flags.StringP(config.FlagYahooSeasons, "S", config.DefaultYahooSeasonsFile, "Yahoo seasons config file (optional, enables Yahoo file checks)")
	config.InitVerboseFlag(flags)
	config.InitIncompleteFlag(flags)
	return cmd
}

// cacheMetrics, seasonResult, simpleSeason, and related functions are defined in metrics.go

func runCacheCheck(cmd *cobra.Command, _ []string) error {
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

	incompleteOnly := viper.GetBool(config.FlagIncomplete)
	if viper.GetBool(config.FlagVerbose) {
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
				s.fileType,
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
		case cache.FileTypeDailySchedule:
			sf.dailySched = [2]int{s.expected, s.found}
		case cache.FileTypeBoxscore:
			sf.boxscore = [2]int{s.expected, s.found}
		case cache.FileTypeLeague:
			sf.league = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case cache.FileTypeTeam:
			sf.team = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case cache.FileTypeRoster:
			sf.roster = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case cache.FileTypeTeamSummary:
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
