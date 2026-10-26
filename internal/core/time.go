package core

import (
	"fmt"
	"time"
)

// HoursPerDay is the number of hours in a calendar day.
const HoursPerDay = 24

// FormatDuration formats a duration for compact human-readable status output.
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

// CountDays returns the number of days between start and end (inclusive).
// If end is before start, returns 0.
func CountDays(start, end time.Time) int {
	days := int(end.Sub(start).Hours()/HoursPerDay) + 1
	if days < 0 {
		return 0
	}
	return days
}
