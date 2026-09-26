package newsadjust

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/news"
)

// CoverageWarnings describes every news source that is not fresh at the
// adjustment's as-of time, for Request.CoverageWarnings: news that was not
// fetched cannot adjust anything, and the manager should know it. The
// coverage comes from news.EvaluateCoverage at the same as-of time.
func CoverageWarnings(coverage []news.SourceCoverage) []string {
	sorted := slices.Clone(coverage)
	slices.SortFunc(sorted, func(a, b news.SourceCoverage) int {
		return cmp.Or(cmp.Compare(a.Source.ID, b.Source.ID), cmp.Compare(a.Scope, b.Scope))
	})
	var warnings []string
	for _, c := range sorted {
		label := fmt.Sprintf("news source %s (%s, %s)", c.Source.ID, c.Source.Publisher, c.Scope)
		switch c.Status {
		case news.CoverageMissing:
			warnings = append(warnings, label+" has never been fetched; its news cannot adjust anything")
		case news.CoverageStale:
			warnings = append(warnings, fmt.Sprintf("%s is stale: its newest data is from %s", label, c.AsOf.UTC().Format(time.RFC3339)))
		case news.CoverageFailing:
			warnings = append(warnings, fmt.Sprintf("%s is failing (%d consecutive failures); its data is from %s",
				label, c.State.ConsecutiveFailures, c.AsOf.UTC().Format(time.RFC3339)))
		}
	}
	return warnings
}
