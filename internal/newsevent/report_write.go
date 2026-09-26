package newsevent

import (
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// reportTimeLayout formats times in the events report.
	reportTimeLayout = "2006-01-02 15:04 MST"
	// unknownValue is shown for a value no report stated.
	unknownValue = "unknown"
	// absenceCaveat is printed wherever an empty list could be read as
	// good news.
	absenceCaveat = "The absence of an event is not evidence that a player is healthy or available."
	// impactCaveat reminds readers what an event is not.
	impactCaveat = "Events restate what the reports say, checked against their quotes. They carry no fantasy impact, " +
		"and events routed to review are not fit for automatic use."
)

// WriteEventDigest renders the events report as Markdown.
func WriteEventDigest(w io.Writer, d EventDigest) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Player news events\n\nGenerated %s. %s\n\n", formatTime(d.GeneratedAt), impactCaveat)
	writeReviews(&b, d.Reviews)
	if d.Player != nil {
		fmt.Fprintf(&b, "## %s: every event\n\n", playerTitle(d.Player.Name, d.Player.NHLPlayerID, d.Player.YahooPlayerID))
		writeEvents(&b, d.PlayerEvents)
	}
	fmt.Fprintf(&b, "## Events reported since %s\n\n", formatTime(d.Since))
	writeEvents(&b, d.Events)
	_, err := io.WriteString(w, b.String())
	return err
}

func writeReviews(b *strings.Builder, reviews []ExtractionReview) {
	b.WriteString("## Extractions to review\n\n")
	if len(reviews) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("| Extraction | Status | Attempts | Article | Publisher | Problems |\n|---|---|---|---|---|---|\n")
	for _, r := range reviews {
		problems := make([]string, 0, len(r.Issues)+1)
		if r.Error != "" {
			problems = append(problems, r.Error)
		}
		for _, is := range r.Issues {
			problems = append(problems, fmt.Sprintf("%s: %s", is.Code, is.Message))
		}
		fmt.Fprintf(b, "| %d (%s) | %s | %d | [%s](%s) | %s | %s |\n", r.ID, cell(r.ExtractorKey), r.Status, r.Attempts,
			cell(r.Title), r.URL, cell(r.Publisher), cell(strings.Join(problems, "; ")))
	}
	b.WriteString("\n")
}

func writeEvents(b *strings.Builder, events []ReportedEvent) {
	if len(events) == 0 {
		fmt.Fprintf(b, "None on record. %s\n\n", absenceCaveat)
		return
	}
	for _, ev := range events {
		writeEvent(b, ev)
	}
}

func writeEvent(b *strings.Builder, ev ReportedEvent) {
	e := ev.Event
	fmt.Fprintf(b, "### Event %d: %s, %s (%s), %s\n\n", ev.ID,
		playerTitle(e.Player.Name, e.Player.NHLPlayerID, e.Player.YahooPlayerID), e.Type, e.Status, ev.Lifecycle)
	fmt.Fprintf(b, "- Duration: %s\n", describeDuration(e.Duration))
	fmt.Fprintf(b, "- Effective from: %s\n", orUnknown(formatDate(e.EffectiveOn)))
	if e.Change.Field != ChangeNone {
		fmt.Fprintf(b, "- Move (%s): %s → %s\n", e.Change.Field, orUnknown(e.Change.From), orUnknown(e.Change.To))
	}
	if e.Attribution != "" {
		fmt.Fprintf(b, "- Attributed to: %s\n", e.Attribution)
	}
	fmt.Fprintf(b, "- Reported: first %s, last %s\n", formatTime(ev.FirstReportedAt), formatTime(ev.LastReportedAt))
	if ev.IncidentID != 0 {
		fmt.Fprintf(b, "- Incident candidate: %d\n", ev.IncidentID)
	}
	if ev.Lifecycle != LifecycleActive {
		fmt.Fprintf(b, "- %s: %s", ev.Lifecycle, ev.LifecycleReason)
		if ev.SupersededBy != 0 {
			fmt.Fprintf(b, " (replaced by event %d)", ev.SupersededBy)
		}
		b.WriteString("\n")
	}
	if e.NeedsReview {
		fmt.Fprintf(b, "- **Needs review:** %s\n", e.ReviewReason)
	}
	for _, evd := range ev.Evidence {
		fmt.Fprintf(b, "- %s: **%s** (%s) [%s](%s), version %d, reported %s, extraction %d\n", evd.Relation, evd.Publisher,
			evd.Kind, cell(evd.Title), evd.URL, evd.Version, formatTime(evd.ReportedAt), evd.ExtractionID)
		for _, q := range evd.Quotes {
			fmt.Fprintf(b, "  > %s\n", cell(q.Text))
		}
	}
	for _, h := range ev.History {
		from := h.FromLifecycle
		if from == "" {
			from = "new"
		}
		fmt.Fprintf(b, "- History %s: %s → %s, %s\n", formatTime(h.At.Time), from, h.ToLifecycle, h.Reason)
	}
	b.WriteString("\n")
}

func describeDuration(d Duration) string {
	switch d.Kind {
	case DurationGames:
		return fmt.Sprintf("%d games", d.Games)
	case DurationDays:
		return fmt.Sprintf("%d days", d.Days)
	case DurationUntilDate:
		return "until " + formatDate(d.Until)
	case "", DurationUnknown:
		return unknownValue + " (no report states a length)"
	default:
		return string(d.Kind)
	}
}

func playerTitle(name string, nhlID int64, yahooID int) string {
	var ids []string
	if nhlID != 0 {
		ids = append(ids, fmt.Sprintf("NHL %d", nhlID))
	}
	if yahooID != 0 {
		ids = append(ids, fmt.Sprintf("Yahoo %d", yahooID))
	}
	return fmt.Sprintf("%s (%s)", name, strings.Join(ids, ", "))
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return unknownValue
	}
	return t.UTC().Format(reportTimeLayout)
}

func orUnknown(s string) string {
	if s == "" {
		return unknownValue
	}
	return s
}

// cell keeps a value on one Markdown line.
func cell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}
