package draftrank

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	tableMinWidth = 0
	tableTabWidth = 4
	tablePadding  = 2
	tablePadChar  = ' '
	tableFlags    = 0
	// tableDecimals is the display precision of the table; exports keep
	// full precision.
	tableDecimals = 2
	percent       = 100
	noValue       = "-"
)

// WriteTable writes a human-readable page: a header naming the league,
// snapshot, scenario and every issue, then one aligned line per row.
func WriteTable(w io.Writer, page Page) error {
	if err := writeTableHeader(w, page); err != nil {
		return err
	}
	if len(page.Rows) == 0 {
		_, err := fmt.Fprintln(w, "No players match.")
		return err
	}
	tw := tabwriter.NewWriter(w, tableMinWidth, tableTabWidth, tablePadding, tablePadChar, tableFlags)
	fmt.Fprintln(tw, "Rank\tPos rank\tBase rank\tChange\tPlayer\tTeam\tPositions\tTier\tValue\tAdj value\tUncertainty\tStatus\tNews")
	for _, row := range page.Rows {
		reasons, alerts := newsCounts(row.Player)
		p := row.Placement
		fmt.Fprintf(tw, "%d\t%d\t%d\t%+d\t%s\t%s\t%s\t%d\t%.*f\t%.*f\t%.0f%%\t%s\t%s\n",
			p.OverallRank, row.PositionRank, row.BaselineRank, row.RankChange, row.Name, row.Team,
			strings.Join(row.EligiblePositions, "/"), p.Tier, tableDecimals, p.Value, tableDecimals, p.AdjustedValue,
			p.Uncertainty*percent, orDash(row.Status), newsLabel(reasons, alerts))
	}
	return tw.Flush()
}

func writeTableHeader(w io.Writer, page Page) error {
	league := page.League
	fmt.Fprintf(w, "%s (%s, season %d): %s\n", orDash(league.Name), orDash(league.LeagueKey), league.Season, page.Status)
	if s := page.Snapshot; s != nil {
		fmt.Fprintf(w, "Snapshot %s computed %s, scenario %s\n", s.ID, s.AsOf.Format(time.RFC3339), page.Scenario)
		fmt.Fprintf(w, "Version %s; projections %s as of %s\n", s.Identity, s.Projection.ModelVersion, s.Projection.AsOf.Format(time.RFC3339))
	}
	for _, issue := range page.Issues {
		fmt.Fprintf(w, "! %s: %s\n", issue.Code, issue.Message)
	}
	_, err := fmt.Fprintf(w, "Showing %d of %d matching players from offset %d\n\n", len(page.Rows), page.Total, page.Offset)
	return err
}

func newsLabel(reasons, alerts int) string {
	if reasons == 0 && alerts == 0 {
		return noValue
	}
	return fmt.Sprintf("%d reasons, %d alerts", reasons, alerts)
}

func orDash(value string) string {
	if value == "" {
		return noValue
	}
	return value
}
