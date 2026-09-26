package newsadjust

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"
)

// Season places event dates on the target season's schedule. Games is the
// number of regular-season games each team plays.
type Season struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Games float64   `json:"games"`
}

const (
	// scheduleAssumption labels the date-to-games approximation.
	scheduleAssumption = "an event date maps to team games elapsed as if games were spread evenly between the season's start and end"
	hoursPerDay        = 24
)

// priorSeason reports whether t belongs to the previous season's news: it
// is more than the policy's offseason window before the opener.
func (s Season) priorSeason(t time.Time, offseasonDays float64) bool {
	window := time.Duration(offseasonDays * hoursPerDay * float64(time.Hour))
	return t.Before(s.Start.Add(-window))
}

func (s Season) validate() error {
	switch {
	case s.Start.IsZero() || !s.End.After(s.Start):
		return fmt.Errorf("season needs a start before its end")
	case math.IsNaN(s.Games) || math.IsInf(s.Games, 0) || s.Games <= 0:
		return fmt.Errorf("season games must be positive")
	}
	return nil
}

// position is the number of team games played by t.
func (s Season) position(t time.Time) float64 {
	if !t.After(s.Start) {
		return 0
	}
	if !t.Before(s.End) {
		return s.Games
	}
	elapsed := t.Sub(s.Start).Seconds() / s.End.Sub(s.Start).Seconds()
	return s.Games * elapsed
}

// interval is a half-open span of team games [From, To).
type interval struct {
	From float64
	To   float64
}

func (i interval) length() float64 {
	return math.Max(0, i.To-i.From)
}

// unionLength totals the games covered by any interval, so overlapping
// absences (an injury during a suspension) are never counted twice.
func unionLength(spans []interval) float64 {
	ordered := make([]interval, 0, len(spans))
	for _, span := range spans {
		if span.length() > 0 {
			ordered = append(ordered, span)
		}
	}
	slices.SortFunc(ordered, func(a, b interval) int { return cmp.Compare(a.From, b.From) })
	total := 0.0
	var current interval
	for i, span := range ordered {
		if i == 0 || span.From > current.To {
			total += current.length()
			current = span
			continue
		}
		current.To = math.Max(current.To, span.To)
	}
	return total + current.length()
}
