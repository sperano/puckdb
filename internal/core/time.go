package core

import "time"

// HoursPerDay is the number of hours in a calendar day.
const HoursPerDay = 24

// CountDays returns the number of days between start and end (inclusive).
// If end is before start, returns 0.
func CountDays(start, end time.Time) int {
	days := int(end.Sub(start).Hours()/HoursPerDay) + 1
	if days < 0 {
		return 0
	}
	return days
}
