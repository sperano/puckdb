package shared

import "fmt"

// BarRef names one bar of a ReportTracker's report.
type BarRef struct {
	Group int
	Bar   int
}

// String formats the reference as "{group G, bar B}".
func (r BarRef) String() string {
	return fmt.Sprintf("{group %d, bar %d}", r.Group, r.Bar)
}

// group returns a pointer to the indexed group, panicking with a message
// that names the group and the report size when idx is out of range. A bad
// index is a programming error in workflow code, so it panics rather than
// returning an error nobody can act on.
func (t *ReportTracker) group(idx int) *ProgressGroup {
	groups := t.report.Groups
	if idx < 0 || idx >= len(groups) {
		panic(fmt.Sprintf("progress: group %d outside report (%d groups)", idx, len(groups)))
	}
	return &groups[idx]
}

// bar returns a pointer to the referenced bar, panicking with a message
// that names the group, bar and sizes when ref is out of range.
func (t *ReportTracker) bar(ref BarRef) *ProgressBar {
	groups := t.report.Groups
	if ref.Group < 0 || ref.Group >= len(groups) {
		panic(fmt.Sprintf("progress: bar %s outside report (%d groups)", ref, len(groups)))
	}
	bars := groups[ref.Group].Bars
	if ref.Bar < 0 || ref.Bar >= len(bars) {
		panic(fmt.Sprintf("progress: bar %s outside report (%d groups; group %d has %d bars)",
			ref, len(groups), ref.Group, len(bars)))
	}
	return &bars[ref.Bar]
}
