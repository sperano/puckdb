package draft

import (
	"fmt"
	"io"
	"strings"
)

// poolReportListLimit caps how many players each gap list names; the counts
// always cover every player.
const poolReportListLimit = 50

// WritePoolReport renders one league's pool coverage as Markdown: counts,
// warnings, and the players without eligibility or without an NHL mapping.
func WritePoolReport(w io.Writer, r LeagueReport) error {
	var b strings.Builder
	c := r.Pool
	fmt.Fprintf(&b, "## %d league %d (%s)\n\n", r.Season, r.LeagueID, r.Snapshot.Rules.LeagueKey)
	if c.Players == 0 {
		b.WriteString("No player pool imported.\n")
	} else {
		fmt.Fprintf(&b, "- Players: %d (fetched %s)\n", c.Players, c.FetchedAt.UTC().Format(timeLayout))
		fmt.Fprintf(&b, "- Eligible at more than one position: %d\n", c.MultiPosition)
		fmt.Fprintf(&b, "- Without Yahoo eligibility: %d\n", len(c.Unresolved))
		fmt.Fprintf(&b, "- Without NHL player mapping: %d\n", len(c.Unmatched))
	}
	for _, warning := range c.Warnings() {
		fmt.Fprintf(&b, "\nWARNING: %s\n", warning)
	}
	writePlayerList(&b, "Players without Yahoo eligibility", c.Unresolved)
	writePlayerList(&b, "Players without an NHL player mapping", c.Unmatched)
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writePlayerList(b *strings.Builder, title string, players []PoolPlayer) {
	if len(players) == 0 {
		return
	}
	fmt.Fprintf(b, "\n### %s\n\n", title)
	for i, p := range players {
		if i == poolReportListLimit {
			fmt.Fprintf(b, "- ... and %d more\n", len(players)-poolReportListLimit)
			return
		}
		fmt.Fprintf(b, "- %s (%s, Yahoo %d, %s)%s\n", p.Name, p.Team, p.YahooPlayerID, positionsLabel(p), statusLabel(p))
	}
}

func positionsLabel(p PoolPlayer) string {
	if len(p.EligiblePositions) == 0 {
		return "no positions"
	}
	return strings.Join(p.EligiblePositions, "/")
}

func statusLabel(p PoolPlayer) string {
	if p.Status == "" {
		return ""
	}
	return " — " + p.StatusFull
}
