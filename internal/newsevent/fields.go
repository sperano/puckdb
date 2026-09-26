package newsevent

import (
	"strconv"
	"time"
)

// effective returns the stated effective date, or zero when the reply gives
// none or its quote does not state it.
func (c *validator) effective(i int, raw *rawDate, reported time.Time) time.Time {
	if raw == nil || raw.Date == "" {
		return time.Time{}
	}
	c.v.Claims++
	date, err := time.Parse(dateLayout, raw.Date)
	if err != nil {
		c.unsupported(i, IssueInvalidValue, "effective_from", "date %q is not YYYY-MM-DD", raw.Date)
		return time.Time{}
	}
	if !c.quoted(i, raw.Quote, "effective_from") {
		return time.Time{}
	}
	if !statesDate(raw.Quote, date, reported) {
		c.unsupported(i, IssueUnsupportedClaim, "effective_from", "quote %q does not state %s", raw.Quote, raw.Date)
		return time.Time{}
	}
	if date.Before(dayStart(reported).Add(-maxEffectivePast)) || date.After(reported.Add(maxEffectiveFuture)) {
		c.unsupported(i, IssueChronology, "effective_from", "%s is implausibly far from the report date %s",
			raw.Date, reported.UTC().Format(dateLayout))
		return time.Time{}
	}
	return date
}

// duration returns the stated length, or unknown when the reply states none
// or its quote does not state the one it gives.
func (c *validator) duration(i int, raw *rawDuration, reported, effective time.Time) Duration {
	unknown := Duration{Kind: DurationUnknown}
	if raw == nil {
		return unknown
	}
	kind := DurationKind(raw.Kind)
	if kind == "" || kind == DurationUnknown {
		if raw.Games != 0 || raw.Days != 0 || raw.Until != "" {
			c.v.Claims++
			c.unsupported(i, IssueUnsupportedClaim, "duration", "a length is given with an unknown duration kind")
		}
		return unknown
	}
	c.v.Claims++
	if !validDurationKinds[kind] {
		c.unsupported(i, IssueInvalidValue, "duration", "duration kind %q is not in the schema", raw.Kind)
		return unknown
	}
	if !c.quoted(i, raw.Quote, "duration") {
		return unknown
	}
	d, code, ok := statedDuration(kind, raw, reported, effective)
	if ok && counted(kind) && !statesAbsence(raw.Quote) {
		// A number of games or days, or an end date, that the quote does
		// not tie to an absence ("started 61 games last season").
		ok, code = false, IssueUnsupportedClaim
	}
	if !ok {
		c.unsupported(i, code, "duration", "quote %q does not state a %s duration %s", raw.Quote, kind, durationValue(raw))
		return unknown
	}
	return d
}

// counted reports whether a duration kind carries a number or a date.
func counted(kind DurationKind) bool {
	return kind == DurationGames || kind == DurationDays || kind == DurationUntilDate
}

// statedDuration checks a duration of a known kind against its quote.
func statedDuration(kind DurationKind, raw *rawDuration, reported, effective time.Time) (Duration, IssueCode, bool) {
	d := Duration{Kind: kind, Quote: raw.Quote}
	switch kind {
	case DurationGames:
		ok := raw.Days == 0 && raw.Until == "" && raw.Games > 0 && raw.Games <= maxStatedGames &&
			statesCount(raw.Quote, raw.Games, "game", "games")
		d.Games = raw.Games
		return d, IssueUnsupportedClaim, ok
	case DurationDays:
		ok := raw.Games == 0 && raw.Until == "" && raw.Days > 0 && raw.Days <= maxStatedDays &&
			(statesCount(raw.Quote, raw.Days, "day", "days") ||
				(raw.Days%daysPerWeek == 0 && statesCount(raw.Quote, raw.Days/daysPerWeek, "week", "weeks")))
		d.Days = raw.Days
		return d, IssueUnsupportedClaim, ok
	case DurationUntilDate:
		return untilDuration(d, raw, reported, effective)
	default:
		ok := raw.Games == 0 && raw.Days == 0 && raw.Until == "" && statesKind(raw.Quote, kind)
		return d, IssueUnsupportedClaim, ok
	}
}

func untilDuration(d Duration, raw *rawDuration, reported, effective time.Time) (Duration, IssueCode, bool) {
	until, err := time.Parse(dateLayout, raw.Until)
	if err != nil || raw.Games != 0 || raw.Days != 0 || !statesDate(raw.Quote, until, reported) {
		return d, IssueUnsupportedClaim, false
	}
	tooEarly := until.Before(dayStart(reported).Add(-maxEffectivePast)) || (!effective.IsZero() && !until.After(effective))
	if tooEarly || until.After(reported.Add(maxUntilFuture)) {
		return d, IssueChronology, false
	}
	d.Until = until
	return d, "", true
}

func durationValue(raw *rawDuration) string {
	switch {
	case raw.Games != 0:
		return "of " + strconv.Itoa(raw.Games) + " games"
	case raw.Days != 0:
		return "of " + strconv.Itoa(raw.Days) + " days"
	case raw.Until != "":
		return "until " + raw.Until
	default:
		return ""
	}
}

// change returns the stated move, with each side cleared when its quote does
// not say it.
func (c *validator) change(i int, raw *rawChange) Change {
	if raw == nil || (raw.Field == "" && raw.From == "" && raw.To == "") {
		return Change{}
	}
	field := ChangeField(raw.Field)
	if !validChangeFields[field] {
		c.v.Claims++
		c.unsupported(i, IssueInvalidValue, "change", "change field %q is not in the schema", raw.Field)
		return Change{}
	}
	if !c.quoted(i, raw.Quote, "change") {
		c.v.Claims++
		return Change{}
	}
	out := Change{Field: field, Quote: raw.Quote}
	out.From = c.side(i, raw.From, raw.Quote, "change.from")
	out.To = c.side(i, raw.To, raw.Quote, "change.to")
	if out.From == "" && out.To == "" {
		return Change{}
	}
	return out
}

func (c *validator) side(i int, value, quote, field string) string {
	if normalize(value) == "" {
		return ""
	}
	c.v.Claims++
	if !containsPhrase(quote, value) {
		c.unsupported(i, IssueUnsupportedClaim, field, "quote %q does not say %q", quote, value)
		return ""
	}
	return value
}
