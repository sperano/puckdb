package news

import (
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// reportTimeLayout formats times in the report.
	reportTimeLayout = "2006-01-02 15:04 MST"
	// reportExcerptRunes bounds the evidence excerpt quoted per report.
	reportExcerptRunes = 280
	// unknownTime is shown for a timestamp the source did not give.
	unknownTime = "unknown"
)

// Digest is everything the news report prints.
type Digest struct {
	GeneratedAt time.Time
	Season      int
	Coverage    []SourceCoverage
	// Player is set when the report was asked about one player.
	Player *PlayerNews
	// Since is the start of the recent-incidents window.
	Since     time.Time
	Incidents []Incident
	Issues    []MentionIssue
}

// WriteDigest renders the digest as Markdown.
func WriteDigest(w io.Writer, r Digest) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Player news\n\nGenerated %s for season %d.\n\n", formatTime(r.GeneratedAt), r.Season)
	writeCoverage(&b, r.Coverage)
	if r.Player != nil {
		writePlayer(&b, *r.Player)
	}
	fmt.Fprintf(&b, "## Incident candidates reported since %s\n\n", formatTime(r.Since))
	if len(r.Incidents) == 0 {
		fmt.Fprintf(&b, "None on record. %s\n\n", absenceCaveat)
	}
	for _, inc := range r.Incidents {
		writeIncident(&b, inc)
	}
	writeIssues(&b, r.Issues)
	_, err := io.WriteString(w, b.String())
	return err
}

func writeCoverage(b *strings.Builder, coverage []SourceCoverage) {
	b.WriteString("## Source coverage\n\n")
	b.WriteString("| Source | Publisher | Kind | Priority | Status | Data as of | Last attempt | Failures | Last error |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	var warnings []string
	for _, c := range coverage {
		fmt.Fprintf(b, "| %s | %s | %s | %d | %s | %s | %s | %d | %s |\n",
			c.Source.ID, c.Source.Publisher, c.Source.Kind, c.Source.Priority, c.Status,
			formatTime(c.AsOf), formatTime(c.State.LastAttemptAt), c.State.ConsecutiveFailures, cell(c.State.LastError))
		if !c.Current() {
			warnings = append(warnings, fmt.Sprintf("%s is %s: its news may be missing or out of date.", c.Source.ID, c.Status))
		}
	}
	b.WriteString("\n")
	for _, warning := range warnings {
		fmt.Fprintf(b, "> **Warning:** %s\n", warning)
	}
	if len(warnings) > 0 {
		b.WriteString("\n")
	}
}

func writePlayer(b *strings.Builder, p PlayerNews) {
	fmt.Fprintf(b, "## %s\n\n", playerLabel(p.Player.Name, p.Player.NHLPlayerID, p.Player.YahooPlayerID))
	fmt.Fprintf(b, "%s\n\n", p.Assessment())
	for _, inc := range p.Incidents {
		writeIncident(b, inc)
	}
}

func writeIncident(b *strings.Builder, inc Incident) {
	fmt.Fprintf(b, "### %s: %s\n\n", playerLabel(inc.PlayerName, inc.NHLPlayerID, inc.YahooPlayerID), inc.Category)
	fmt.Fprintf(b, "First reported %s, last reported %s. Independent sources: %s; repeated reports: %d.\n\n",
		formatTime(inc.FirstReportedAt), formatTime(inc.LastReportedAt),
		orNone(strings.Join(inc.IndependentPublishers(), ", ")), len(inc.Evidence)-len(inc.IndependentPublishers()))
	if inc.Superseded() {
		b.WriteString("> Every report behind this incident has a newer version that no longer supports it (corrected or updated).\n\n")
	}
	for _, e := range inc.Evidence {
		writeEvidence(b, e)
	}
	b.WriteString("\n")
}

func writeEvidence(b *strings.Builder, e Evidence) {
	fmt.Fprintf(b, "- **%s** (%s, %s) [%s](%s): published %s, updated %s, retrieved %s",
		e.Publisher, e.Kind, e.Relation, cell(e.Title), e.URL,
		formatTime(e.PublishedAt), formatTime(e.SourceUpdatedAt), formatTime(e.RetrievedAt))
	if e.Author != "" {
		fmt.Fprintf(b, "; by %s", e.Author)
	}
	if e.Outdated() {
		fmt.Fprintf(b, "; version %d of %d", e.Version, e.LatestVersion)
	}
	b.WriteString("\n")
	if e.Text != "" {
		fmt.Fprintf(b, "  > %s\n", CleanText(e.Text, reportExcerptRunes))
	}
}

func writeIssues(b *strings.Builder, issues []MentionIssue) {
	b.WriteString("## Unattached story subjects\n\n")
	if len(issues) == 0 {
		b.WriteString("None.\n")
		return
	}
	b.WriteString("These stories name a player who could not be identified with certainty; they are not attached to anyone.\n\n")
	b.WriteString("| Name | Resolution | Reason | Candidates | Story | Publisher | Retrieved |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, is := range issues {
		candidates := make([]string, 0, len(is.Candidates))
		for _, c := range is.Candidates {
			candidates = append(candidates, playerLabel(c.Name, c.NHLPlayerID, c.YahooPlayerID)+teamSuffix(c.Team))
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | [%s](%s) | %s | %s |\n",
			cell(is.Mention), is.Resolution, is.Method, cell(orNone(strings.Join(candidates, "; "))),
			cell(is.Title), is.URL, is.Publisher, formatTime(is.RetrievedAt))
	}
}

func playerLabel(name string, nhlID int64, yahooID int) string {
	var ids []string
	if nhlID != 0 {
		ids = append(ids, fmt.Sprintf("NHL %d", nhlID))
	}
	if yahooID != 0 {
		ids = append(ids, fmt.Sprintf("Yahoo %d", yahooID))
	}
	return fmt.Sprintf("%s (%s)", name, strings.Join(ids, ", "))
}

func teamSuffix(team string) string {
	if team == "" {
		return ""
	}
	return " " + team
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return unknownTime
	}
	return t.UTC().Format(reportTimeLayout)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// cell keeps a value on one Markdown table line.
func cell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}
