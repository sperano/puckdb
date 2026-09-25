package draft

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	missingCell     = "—"
	comparisonTitle = "# Yahoo league rules comparison"
	timeLayout      = "2006-01-02 15:04 MST"
	hoursPerDay     = 24
)

// ComparisonOptions controls how a comparison is rendered.
type ComparisonOptions struct {
	GeneratedAt time.Time
	// MaxAge is how old settings or a pool may be before they are flagged.
	MaxAge time.Duration
}

// WriteComparison renders the leagues' rules side by side as Markdown:
// league facts, scoring categories, roster slots, remaining settings and a
// warning list per league. Nothing is filled in from defaults; a rule a
// league does not have shows as "—".
func WriteComparison(w io.Writer, reports []LeagueReport, opts ComparisonOptions) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nGenerated %s. Rules older than %s are flagged stale.\n",
		comparisonTitle, opts.GeneratedAt.UTC().Format(timeLayout), formatAge(opts.MaxAge))
	writeLeagueTable(&b, reports, opts)
	writeCategoryTable(&b, reports)
	writeSlotTable(&b, reports)
	writeSettingsTable(&b, reports)
	writeWarnings(&b, reports, opts)
	_, err := io.WriteString(w, b.String())
	return err
}

func writeTableHeader(b *strings.Builder, first string, reports []LeagueReport) {
	fmt.Fprintf(b, "| %s |", first)
	for _, r := range reports {
		fmt.Fprintf(b, " %d league %d |", r.Season, r.LeagueID)
	}
	b.WriteString("\n|---|")
	b.WriteString(strings.Repeat("---|", len(reports)))
	b.WriteString("\n")
}

func writeRow(b *strings.Builder, label string, cells []string) {
	fmt.Fprintf(b, "| %s |", escapeCell(label))
	for _, cell := range cells {
		fmt.Fprintf(b, " %s |", escapeCell(cell))
	}
	b.WriteString("\n")
}

func escapeCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

func cellsFor(reports []LeagueReport, cell func(LeagueReport) string) []string {
	cells := make([]string, len(reports))
	for i, r := range reports {
		cells[i] = cell(r)
	}
	return cells
}

func writeLeagueTable(b *strings.Builder, reports []LeagueReport, opts ComparisonOptions) {
	b.WriteString("\n## Leagues\n\n")
	writeTableHeader(b, "", reports)
	rows := []struct {
		label string
		cell  func(LeagueReport) string
	}{
		{"Name", func(r LeagueReport) string { return r.Snapshot.Rules.Name }},
		{"League key", func(r LeagueReport) string { return r.Snapshot.Rules.LeagueKey }},
		{"Rules source", sourceCell},
		{"Settings fetched", func(r LeagueReport) string { return fetchedCell(r.Snapshot.FetchedAt, opts) }},
		{"Rule versions seen", versionsCell},
		{"Scoring", scoringCell},
		{"Teams", func(r LeagueReport) string { return strconv.Itoa(r.Snapshot.Rules.NumTeams) }},
		{"Your team", ownedTeamCell},
		{"Draft", draftCell},
		{"Draft time", draftTimeCell},
		{"Player pool", func(r LeagueReport) string { return poolCell(r.Pool, opts) }},
	}
	for _, row := range rows {
		writeRow(b, row.label, cellsFor(reports, row.cell))
	}
}

func sourceCell(r LeagueReport) string {
	if r.Snapshot.Source == SourceYahooAPI {
		return "Yahoo API (league's own settings)"
	}
	return fmt.Sprintf("UNVERIFIED temporary stand-in: %d settings of %s",
		r.Snapshot.SourceSeason, r.Snapshot.SourceLeagueKey)
}

func fetchedCell(fetched time.Time, opts ComparisonOptions) string {
	cell := fetchedAge(fetched, opts.GeneratedAt)
	if opts.GeneratedAt.Sub(fetched) > opts.MaxAge {
		cell += ", STALE"
	}
	return cell
}

func fetchedAge(fetched, now time.Time) string {
	return fmt.Sprintf("%s (%s ago)", fetched.UTC().Format(timeLayout), formatAge(now.Sub(fetched)))
}

func formatAge(d time.Duration) string {
	if d >= hoursPerDay*time.Hour {
		return fmt.Sprintf("%dd", int(d.Hours())/hoursPerDay)
	}
	return d.Truncate(time.Minute).String()
}

func versionsCell(r LeagueReport) string {
	return fmt.Sprintf("%d (this one first seen %s)", r.Snapshot.Versions, r.Snapshot.FirstSeenAt.UTC().Format(timeLayout))
}

func scoringCell(r LeagueReport) string {
	kind, known := scoringTypes[r.Snapshot.Rules.ScoringType]
	if !known {
		return fmt.Sprintf("%s (unsupported)", r.Snapshot.Rules.ScoringType)
	}
	return fmt.Sprintf("%s (%s, %s)", r.Snapshot.Rules.ScoringType, kind.format, kind.objective)
}

func ownedTeamCell(r LeagueReport) string {
	if r.OwnedTeam == nil {
		return "unknown"
	}
	cell := fmt.Sprintf("%s (team %d", r.OwnedTeam.Name, r.OwnedTeam.TeamID)
	if r.OwnedTeam.DraftPosition > 0 {
		cell += fmt.Sprintf(", draft position %d", r.OwnedTeam.DraftPosition)
	}
	return cell + ")"
}

func draftCell(r LeagueReport) string {
	d := r.Snapshot.Rules.Draft
	auction := "no"
	if d.Auction {
		auction = "yes"
	}
	status := d.Status
	if status == "" {
		status = "unknown"
	}
	cell := fmt.Sprintf("%s, auction: %s, status %s", d.Type, auction, status)
	if d.PickTimeSeconds > 0 {
		cell += fmt.Sprintf(", %ds per pick", d.PickTimeSeconds)
	}
	return cell
}

func draftTimeCell(r LeagueReport) string {
	if r.Snapshot.Rules.Draft.Time == nil {
		return "not scheduled"
	}
	return r.Snapshot.Rules.Draft.Time.UTC().Format(timeLayout)
}

func poolCell(c PoolCoverage, opts ComparisonOptions) string {
	if c.Players == 0 {
		return "not imported"
	}
	return fmt.Sprintf("%d players (%d multi-position, %d without eligibility, %d unmatched to NHL), fetched %s",
		c.Players, c.MultiPosition, len(c.Unresolved), len(c.Unmatched), fetchedCell(c.FetchedAt, opts))
}

// sortedKeys returns the union of keys across leagues in ascending order.
func sortedKeys[K interface{ ~int | ~string }](reports []LeagueReport, keys func(LeagueReport) []K) []K {
	var all []K
	for _, r := range reports {
		for _, k := range keys(r) {
			if !slices.Contains(all, k) {
				all = append(all, k)
			}
		}
	}
	slices.Sort(all)
	return all
}
