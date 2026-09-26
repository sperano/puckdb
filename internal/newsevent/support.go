package newsevent

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/news"
)

const (
	// minQuoteWords is the shortest quote accepted as evidence: shorter
	// ones ("was suspended") match almost any hockey story.
	minQuoteWords = 3
	// daysPerWeek converts a length stated in weeks to days.
	daysPerWeek = 7
	// dayTolerance allows for a report dated in UTC while the article's
	// "today" is local time.
	dayTolerance = 24 * time.Hour
	// weekdayReach bounds how far a bare weekday ("Tuesday") may be from the
	// report date.
	weekdayReach = 7 * 24 * time.Hour
	// hoursPerDay converts day counts to durations.
	hoursPerDay = 24
)

// normalize lowercases s, strips accents and punctuation, and joins its
// words with single spaces: quotes match regardless of typographic quotes,
// dashes and spacing.
func normalize(s string) string {
	return strings.Join(news.Words(s), " ")
}

// containsWords reports whether the words of needle appear consecutively in
// haystack's words.
func containsWords(haystack, needle []string) bool {
	return indexWords(haystack, needle) >= 0
}

// indexWords returns the first position of needle's words in haystack, or -1.
func indexWords(haystack, needle []string) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

// containsPhrase reports whether phrase's words appear consecutively in text.
func containsPhrase(text, phrase string) bool {
	return containsWords(news.Words(text), news.Words(phrase))
}

// numberWords are the spelled-out numbers a length may use. "a" and "an"
// are left out: "hurt in a game" states no count.
var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8,
	"nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
	"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20, "thirty": 30,
	"forty": 40, "fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
}

const (
	// minTens and maxUnit bound the compound spelled numbers
	// ("twenty five").
	minTens = 20
	maxUnit = 9
)

// numberAt reads a number at words[i]: digits, or spelled out, including
// compounds. It returns the value and the words it spans (0 when none).
func numberAt(words []string, i int) (int, int) {
	if n, err := strconv.Atoi(words[i]); err == nil {
		return n, 1
	}
	n, ok := numberWords[words[i]]
	if !ok {
		return 0, 0
	}
	if n >= minTens && n%10 == 0 && i+1 < len(words) {
		if unit, ok := numberWords[words[i+1]]; ok && unit >= 1 && unit <= maxUnit {
			return n + unit, 2
		}
	}
	return n, 1
}

// maxUnitGap is how many words may separate a count from its unit ("three
// preseason and regular-season games").
const maxUnitGap = 4

// statesCount reports whether quote states n followed by one of units,
// directly or across a few words with no other number or preposition
// ("five games", "5-game", "two weeks", "three preseason and regular-season
// games"; not "2 of the last 5 games" or "5 goals in the game").
func statesCount(quote string, n int, units ...string) bool {
	words := news.Words(quote)
	for i := range words {
		value, width := numberAt(words, i)
		if width == 0 || value != n {
			continue
		}
		if unitFollows(words, i+width, units) {
			return true
		}
	}
	return false
}

// gapStops end the words a count may skip to reach its unit: "5 goals in
// the game" counts goals, not games.
var gapStops = []string{
	"in", "of", "at", "on", "against", "into", "during", "with", "for", "from", "by", "over", "per", "after", "before",
}

// unitFollows reports whether a unit comes within maxUnitGap words from
// start, before any other number or a preposition.
func unitFollows(words []string, start int, units []string) bool {
	for j := start; j < len(words) && j <= start+maxUnitGap; j++ {
		if slices.Contains(units, words[j]) {
			return true
		}
		if _, width := numberAt(words, j); width > 0 || slices.Contains(gapStops, words[j]) {
			return false
		}
	}
	return false
}

var monthNames = map[string]time.Month{
	"january": time.January, "jan": time.January, "february": time.February, "feb": time.February,
	"march": time.March, "mar": time.March, "april": time.April, "apr": time.April, "may": time.May,
	"june": time.June, "jun": time.June, "july": time.July, "jul": time.July, "august": time.August,
	"aug": time.August, "september": time.September, "sept": time.September, "sep": time.September,
	"october": time.October, "oct": time.October, "november": time.November, "nov": time.November,
	"december": time.December, "dec": time.December,
}

// relativeDays are words that place a date relative to the report date.
var relativeDays = map[string]int{
	"today": 0, "tonight": 0, "immediately": 0, "tomorrow": 1, "yesterday": -1,
}

var weekdayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

// statesDate reports whether quote states date: as a month and day
// ("Oct. 5", "5 October"), an ISO date, a word relative to the report date
// ("today", "effective immediately", "tomorrow"), or a weekday within a week
// of it.
func statesDate(quote string, date, reported time.Time) bool {
	words := news.Words(quote)
	for i, w := range words {
		if month, ok := monthNames[w]; ok && month == date.Month() && dayNear(words, i, date.Day()) {
			return true
		}
		if offset, ok := relativeDays[w]; ok && sameDay(date, reported.AddDate(0, 0, offset)) {
			return true
		}
		if day, ok := weekdayNames[w]; ok && day == date.Weekday() && absDuration(date.Sub(reported)) <= weekdayReach {
			return true
		}
	}
	iso := news.Words(date.Format(dateLayout))
	return containsWords(words, iso)
}

// dayNear reports whether the word before or after a month name is day.
func dayNear(words []string, i, day int) bool {
	for _, j := range []int{i - 1, i + 1} {
		if j >= 0 && j < len(words) && ordinalValue(words[j]) == day {
			return true
		}
	}
	return false
}

// ordinalValue reads "5", "5th", "21st"; 0 when w is not a day number.
func ordinalValue(w string) int {
	for _, suffix := range []string{"st", "nd", "rd", "th"} {
		w = strings.TrimSuffix(w, suffix)
	}
	n, err := strconv.Atoi(w)
	if err != nil {
		return 0
	}
	return n
}

func sameDay(a, b time.Time) bool {
	return absDuration(dayStart(a).Sub(dayStart(b))) <= dayTolerance
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// absenceWords tie a counted length to the player's absence: "suspended
// five games" and "will miss two weeks" state a length, "started 61 games
// last season" does not.
var absenceWords = []string{
	"miss", "misses", "missed", "missing", "out", "suspended", "suspension", "suspend", "sidelined",
	"sit", "sits", "banned", "ban", "reserve", "absence", "absent", "without", "shelved",
}

// statesAbsence reports whether quote speaks of the player being unavailable.
func statesAbsence(quote string) bool {
	words := news.Words(quote)
	return slices.ContainsFunc(words, func(w string) bool { return slices.Contains(absenceWords, w) })
}

// kindPhrases are the words that state each open-ended duration kind.
var kindPhrases = map[DurationKind][]string{
	DurationIndefinite:   {"indefinitely", "indefinite", "until further notice", "no timetable", "no timeline"},
	DurationDayToDay:     {"day to day"},
	DurationWeekToWeek:   {"week to week"},
	DurationMonthToMonth: {"month to month"},
	DurationSeason: {
		"rest of the season", "remainder of the season", "for the season", "season ending", "entire season",
		"out for the year", "rest of the year",
	},
}

// statesKind reports whether quote states an open-ended duration kind.
func statesKind(quote string, kind DurationKind) bool {
	for _, phrase := range kindPhrases[kind] {
		if containsPhrase(quote, phrase) {
			return true
		}
	}
	return false
}
