package yahooaccess

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReport_Subject(t *testing.T) {
	tests := []struct {
		name   string
		report Report
		want   string
	}{
		{
			name:   "authorized",
			report: Report{Season: testSeason, CheckedAt: testCheckedAt, Outcome: Authorized, LeaguesServed: 2, LeaguesChecked: 2},
			want:   "[puckdb] Yahoo app AUTHORIZED for 2026 (2/2 leagues) - 2026-10-01",
		},
		{
			name:   "not authorized",
			report: Report{Season: testSeason, CheckedAt: testCheckedAt, Outcome: NotAuthorized, LeaguesChecked: 2},
			want:   "[puckdb] Yahoo app NOT authorized yet for 2026 (0/2 leagues) - 2026-10-01",
		},
		{
			name:   "error",
			report: FailedReport(testSeason, testCheckedAt, errors.New("no token")),
			want:   "[puckdb] Yahoo access check ERROR - 2026-10-01",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.report.Subject())
		})
	}
}

func TestReport_Text(t *testing.T) {
	report := Report{
		Season:    testSeason,
		CheckedAt: testCheckedAt,
		Outcome:   NotAuthorized,
		Probes: []Probe{
			{Name: "game key", URL: gameKeyURL, StatusCode: http.StatusForbidden, Detail: notAuthorized},
			{Name: "league 111 settings", Skipped: true, Detail: "not checked: game key unknown"},
		},
		LeaguesChecked: 1,
	}

	want := "Yahoo Fantasy API access for the 2026 season: NOT AUTHORIZED\n" +
		"Checked at 2026-10-01T12:00:00Z.\n\n" +
		"- game key: 403 Forbidden - " + notAuthorized + "\n" +
		"  " + gameKeyURL + "\n" +
		"- league 111 settings: not checked: game key unknown\n\n" +
		"Yahoo still refuses the app (403). Nothing to do until the next run.\n"
	require.Equal(t, want, report.Text())
}

func TestReport_TextOfFailedStart(t *testing.T) {
	text := FailedReport(testSeason, testCheckedAt, errors.New("no oauth2 token found in redis")).Text()
	require.Contains(t, text, "The check could not run: no oauth2 token found in redis\n")
	require.Contains(t, text, "sign in again at /yahoo/login")
}
