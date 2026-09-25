package draft

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func writeCategoryTable(b *strings.Builder, reports []LeagueReport) {
	b.WriteString("\n## Scoring categories\n\n")
	b.WriteString("Display-only stats are shown by Yahoo but never scored.\n\n")
	writeTableHeader(b, "Stat", reports)
	statIDs := sortedKeys(reports, func(r LeagueReport) []int {
		ids := make([]int, 0, len(r.Snapshot.Rules.Categories))
		for _, c := range r.Snapshot.Rules.Categories {
			ids = append(ids, c.StatID)
		}
		return ids
	})
	for _, id := range statIDs {
		writeRow(b, categoryLabelFor(reports, id), cellsFor(reports, func(r LeagueReport) string {
			category, found := findCategory(r.Snapshot.Rules, id)
			if !found {
				return missingCell
			}
			return categoryCell(category, r.Snapshot.Rules.ScoringType)
		}))
	}
}

// categoryLabelFor labels a stat row from the first league that has it.
func categoryLabelFor(reports []LeagueReport, statID int) string {
	for _, r := range reports {
		if category, found := findCategory(r.Snapshot.Rules, statID); found {
			return categoryLabel(category)
		}
	}
	return ""
}

func findCategory(rules Rules, statID int) (StatCategory, bool) {
	for _, c := range rules.Categories {
		if c.StatID == statID {
			return c, true
		}
	}
	return StatCategory{}, false
}

func categoryLabel(c StatCategory) string {
	label := fmt.Sprintf("%s — %s (stat %d", c.Label(), c.Name, c.StatID)
	if len(c.PositionTypes) > 0 {
		label += ", " + strings.Join(c.PositionTypes, "/")
	}
	return label + ")"
}

func categoryCell(c StatCategory, scoringType string) string {
	switch {
	case c.DisplayOnly:
		return "display only"
	case !c.Enabled:
		return "disabled"
	case scoringTypes[scoringType].format == FormatPoints:
		return pointsCell(c)
	default:
		return directionCell(c.Direction)
	}
}

func pointsCell(c StatCategory) string {
	if c.Weight == nil {
		return "NO WEIGHT"
	}
	cell := strconv.FormatFloat(*c.Weight, 'f', -1, 64) + " pts"
	if len(c.Bonuses) > 0 {
		cell += " + bonuses (unsupported)"
	}
	return cell
}

func directionCell(d Direction) string {
	switch d {
	case HigherIsBetter:
		return "higher is better"
	case LowerIsBetter:
		return "lower is better"
	default:
		return "DIRECTION UNKNOWN"
	}
}

func writeSlotTable(b *strings.Builder, reports []LeagueReport) {
	b.WriteString("\n## Roster slots\n\n")
	writeTableHeader(b, "Slot", reports)
	var positions []string
	for _, r := range reports {
		for _, s := range r.Snapshot.Rules.RosterSlots {
			if !slices.Contains(positions, s.Position) {
				positions = append(positions, s.Position)
			}
		}
	}
	for _, position := range positions {
		writeRow(b, slotLabel(position), cellsFor(reports, func(r LeagueReport) string {
			for _, s := range r.Snapshot.Rules.RosterSlots {
				if s.Position == position {
					return strconv.Itoa(s.Count)
				}
			}
			return missingCell
		}))
	}
	writeRow(b, "Total slots / starting slots", cellsFor(reports, func(r LeagueReport) string {
		total, starting := 0, 0
		for _, s := range r.Snapshot.Rules.RosterSlots {
			total += s.Count
			if s.Starting {
				starting += s.Count
			}
		}
		return fmt.Sprintf("%d / %d", total, starting)
	}))
}

func slotLabel(position string) string {
	switch kind := (RosterSlot{Position: position}).Kind(); {
	case flexSlotPositions[position] != nil:
		return fmt.Sprintf("%s (flex: %s)", position, strings.Join(flexSlotPositions[position], "/"))
	case kind == SlotKindBench:
		return position + " (bench)"
	case kind == SlotKindReserve:
		return position + " (reserve: Yahoo-eligible players only)"
	case kind == SlotKindUnsupported:
		return position + " (NOT MODELED)"
	default:
		return position
	}
}

func writeSettingsTable(b *strings.Builder, reports []LeagueReport) {
	b.WriteString("\n## Other league settings (as Yahoo reports them)\n\n")
	writeTableHeader(b, "Setting", reports)
	keys := sortedKeys(reports, func(r LeagueReport) []string {
		keys := make([]string, 0, len(r.Snapshot.Rules.Settings))
		for k := range r.Snapshot.Rules.Settings {
			keys = append(keys, k)
		}
		return keys
	})
	for _, key := range keys {
		writeRow(b, key, cellsFor(reports, func(r LeagueReport) string {
			if value, found := r.Snapshot.Rules.Settings[key]; found {
				return value
			}
			return missingCell
		}))
	}
}

func writeWarnings(b *strings.Builder, reports []LeagueReport, opts ComparisonOptions) {
	b.WriteString("\n## Warnings\n")
	for _, r := range reports {
		fmt.Fprintf(b, "\n### %d league %d\n\n", r.Season, r.LeagueID)
		warnings := ReportWarnings(r, opts)
		if len(warnings) == 0 {
			b.WriteString("None.\n")
			continue
		}
		for _, w := range warnings {
			fmt.Fprintf(b, "- %s\n", w)
		}
	}
}

// ReportWarnings lists everything that makes a league's rules or pool
// unverified, stale, incomplete or unsupported.
func ReportWarnings(r LeagueReport, opts ComparisonOptions) []string {
	var warnings []string
	s := r.Snapshot
	if s.Source != SourceYahooAPI {
		warnings = append(warnings, fmt.Sprintf("UNVERIFIED: rules are a TEMPORARY stand-in copied from %d league %s; "+
			"Yahoo has not served this league's own settings", s.SourceSeason, s.SourceLeagueKey))
	}
	if opts.GeneratedAt.Sub(s.FetchedAt) > opts.MaxAge {
		warnings = append(warnings, "settings are stale: fetched "+fetchedAge(s.FetchedAt, opts.GeneratedAt))
	}
	if _, err := ScoringFor(s); err != nil {
		warnings = append(warnings, scoringWarning(err))
	}
	warnings = append(warnings, ruleWarnings(s.Rules)...)
	warnings = append(warnings, r.Notes...)
	if s.Source != SourceYahooAPI && r.Pool.Players == 0 {
		return append(warnings, "no player pool: a stand-in league makes no Yahoo calls, so eligibility and "+
			"status stay unavailable until Yahoo serves the league")
	}
	return append(warnings, r.Pool.Warnings()...)
}

func scoringWarning(err error) string {
	if errors.Is(err, ErrMissingScoringInputs) {
		return "rankings will refuse this league: " + err.Error()
	}
	return err.Error()
}

func ruleWarnings(rules Rules) []string {
	var warnings []string
	for _, slot := range rules.RosterSlots {
		if slot.Kind() == SlotKindUnsupported {
			warnings = append(warnings, fmt.Sprintf("roster slot %q is not modeled; roster checks leave it to players Yahoo lists for it", slot.Position))
		}
	}
	for _, name := range rules.UnmodeledSettings {
		warnings = append(warnings, fmt.Sprintf("nested setting <%s> is not modeled", name))
	}
	warnings = append(warnings, rules.Issues...)
	if rules.Draft.Time == nil {
		warnings = append(warnings, "no scheduled draft time")
	}
	return warnings
}
