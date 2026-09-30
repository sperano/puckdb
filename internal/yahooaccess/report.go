package yahooaccess

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	subjectPrefix = "[puckdb]"
	dateLayout    = time.DateOnly

	// failedAdvice is the next step when the check could not decide.
	failedAdvice = "The check could not tell whether the app is authorized. If the token is missing " +
		"or was rejected, sign in again at /yahoo/login."
)

// Subject is the email subject: it carries each season's outcome, so it can be
// read without opening the email, and the date, so mail clients do not thread
// every day's report into one conversation.
func (r Report) Subject() string {
	date := r.CheckedAt.UTC().Format(dateLayout)
	if r.Error != "" || len(r.Seasons) == 0 {
		return fmt.Sprintf("%s Yahoo access check ERROR - %s", subjectPrefix, date)
	}
	verdicts := make([]string, 0, len(r.Seasons))
	for _, season := range r.Seasons {
		verdicts = append(verdicts, season.verdict())
	}
	return fmt.Sprintf("%s Yahoo app: %s - %s", subjectPrefix, strings.Join(verdicts, "; "), date)
}

// verdict is one season's compact subject phrase.
func (s SeasonResult) verdict() string {
	switch s.Outcome {
	case Authorized:
		return fmt.Sprintf("%d AUTHORIZED (%d/%d leagues)", s.Season, s.LeaguesServed, s.LeaguesChecked)
	case NotAuthorized:
		return fmt.Sprintf("%d NOT authorized yet (%d/%d leagues)", s.Season, s.LeaguesServed, s.LeaguesChecked)
	default:
		return fmt.Sprintf("%d ERROR", s.Season)
	}
}

// Text is the plain-text report: every checked season, each call and what to do
// next.
func (r Report) Text() string {
	var b strings.Builder
	b.WriteString("Yahoo Fantasy API access check\n")
	fmt.Fprintf(&b, "Checked at %s.\n", r.CheckedAt.UTC().Format(time.RFC3339))
	if r.Error != "" {
		fmt.Fprintf(&b, "\nThe check could not run: %s\n", r.Error)
	}
	for _, season := range r.Seasons {
		fmt.Fprintf(&b, "\n%d season: %s\n", season.Season, season.Outcome)
		for _, p := range season.Probes {
			fmt.Fprintf(&b, "- %s: %s\n", p.Name, p.summary())
			if p.URL != "" {
				fmt.Fprintf(&b, "  %s\n", p.URL)
			}
		}
	}
	b.WriteString("\n")
	b.WriteString(r.nextStep())
	b.WriteString("\n")
	return b.String()
}

func (p Probe) summary() string {
	if p.Skipped || p.StatusCode == 0 {
		return p.Detail
	}
	status := fmt.Sprintf("%d %s", p.StatusCode, http.StatusText(p.StatusCode))
	if p.Detail == "" {
		return status
	}
	return status + " - " + p.Detail
}

// nextStep is what to do next, based on the requested season.
func (r Report) nextStep() string {
	primary, ok := r.Primary()
	if !ok || primary.Outcome == Failed {
		return failedAdvice
	}
	if primary.Outcome == Authorized {
		return fmt.Sprintf("Yahoo serves the %d leagues. Remove temporary_metadata_from from them in the "+
			"Yahoo seasons config, run a sync so the real settings replace the stand-in, "+
			"then turn this check off.", primary.Season)
	}
	return "Yahoo still refuses the app (403). Nothing to do until the next run."
}
