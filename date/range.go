package date

import (
	"errors"
	"time"
)

type DateRange []time.Time

func NewDateRange(start time.Time, end time.Time) DateRange {
	// TODO make with size since we know (end - start days)
	dr := DateRange{}
	if end.Before(start) {
		return dr
	}
	d := start
	dr = append(dr, d)
	if d.Day() == end.Day() && d.Month() == end.Month() && d.Year() == end.Year() {
		return dr
	}
	for {
		d = d.AddDate(0, 0, 1)
		dr = append(dr, d)
		if d.Day() == end.Day() && d.Month() == end.Month() && d.Year() == end.Year() {
			break
		}
	}
	return dr
}

var ErrBeforeSeasonStart = errors.New("date is before the season start")

func DateRangeToToday(start time.Time, end time.Time, today time.Time) (DateRange, error) {
	// TODO no error, just empty range
	if today.Before(start) {
		return nil, ErrBeforeSeasonStart
	}
	if today.Before(end) {
		today = today.AddDate(0, 0, -1)
	} else {
		if today.After(end) {
			today = end
		}
	}
	return NewDateRange(start, today), nil
}
