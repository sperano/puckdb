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
)

// Subject is the email subject: it carries the outcome, so it can be read
// without opening the email, and the date, so mail clients do not thread
// every day's report into one conversation.
func (r Report) Subject() string {
	date := r.CheckedAt.UTC().Format(dateLayout)
	switch r.Outcome {
	case Authorized:
		return fmt.Sprintf("%s Yahoo app AUTHORIZED for %d (%d/%d leagues) - %s",
			subjectPrefix, r.Season, r.LeaguesServed, r.LeaguesChecked, date)
	case NotAuthorized:
		return fmt.Sprintf("%s Yahoo app NOT authorized yet for %d (%d/%d leagues) - %s",
			subjectPrefix, r.Season, r.LeaguesServed, r.LeaguesChecked, date)
	default:
		return fmt.Sprintf("%s Yahoo access check ERROR - %s", subjectPrefix, date)
	}
}

// Text is the plain-text report: the outcome, each call and what to do next.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Yahoo Fantasy API access for the %d season: %s\n", r.Season, r.Outcome)
	fmt.Fprintf(&b, "Checked at %s.\n\n", r.CheckedAt.UTC().Format(time.RFC3339))
	if r.Error != "" {
		fmt.Fprintf(&b, "The check could not run: %s\n\n", r.Error)
	}
	for _, p := range r.Probes {
		fmt.Fprintf(&b, "- %s: %s\n", p.Name, p.summary())
		if p.URL != "" {
			fmt.Fprintf(&b, "  %s\n", p.URL)
		}
	}
	if len(r.Probes) > 0 {
		b.WriteString("\n")
	}
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

func (r Report) nextStep() string {
	switch r.Outcome {
	case Authorized:
		return fmt.Sprintf("Yahoo serves the %d leagues. Remove temporary_metadata_from from them in the "+
			"Yahoo seasons config, run a sync so the real settings replace the stand-in, "+
			"then turn this check off.", r.Season)
	case NotAuthorized:
		return "Yahoo still refuses the app (403). Nothing to do until the next run."
	default:
		return "The check could not tell whether the app is authorized. If the token is missing " +
			"or was rejected, sign in again at /yahoo/login."
	}
}
