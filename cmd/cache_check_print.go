package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize/english"
	"github.com/sperano/puckdb/store"
)

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
		case store.FileTypeDailySchedule:
			sf.dailySched = [2]int{s.expected, s.found}
		case store.FileTypeBoxscore:
			sf.boxscore = [2]int{s.expected, s.found}
		case store.FileTypeLeague:
			sf.league = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case store.FileTypeTeam:
			sf.team = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case store.FileTypeRoster:
			sf.roster = [2]int{s.expected, s.found}
			sf.hasYahoo = true
		case store.FileTypeTeamSummary:
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
