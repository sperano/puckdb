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
			report: Report{CheckedAt: testCheckedAt, Seasons: []SeasonResult{{Season: testSeason, Outcome: Authorized, LeaguesServed: 2, LeaguesChecked: 2}}},
			want:   "[puckdb] Yahoo app: 2026 AUTHORIZED (2/2 leagues) - 2026-10-01",
		},
		{
			name:   "not authorized",
			report: Report{CheckedAt: testCheckedAt, Seasons: []SeasonResult{{Season: testSeason, Outcome: NotAuthorized, LeaguesChecked: 2}}},
			want:   "[puckdb] Yahoo app: 2026 NOT authorized yet (0/2 leagues) - 2026-10-01",
		},
		{
			name: "control season",
			report: Report{CheckedAt: testCheckedAt, Seasons: []SeasonResult{
				{Season: testSeason, Outcome: NotAuthorized, LeaguesChecked: 2},
				{Season: testPreviousSeason, Outcome: Authorized, LeaguesServed: 1, LeaguesChecked: 1},
			}},
			want: "[puckdb] Yahoo app: 2026 NOT authorized yet (0/2 leagues); 2025 AUTHORIZED (1/1 leagues) - 2026-10-01",
		},
		{
			name:   "failed season",
			report: Report{CheckedAt: testCheckedAt, Seasons: []SeasonResult{{Season: testSeason, Outcome: Failed}}},
			want:   "[puckdb] Yahoo app: 2026 ERROR - 2026-10-01",
		},
		{
			name:   "error",
			report: FailedReport(testCheckedAt, errors.New("no token")),
			want:   "[puckdb] Yahoo access check ERROR - 2026-10-01",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.report.Subject())
		})
	}
}

// The requested season drives the verdict and the exit code; the control
// season is diagnostic only.
func TestReport_Outcome(t *testing.T) {
	tests := map[string]struct {
		report Report
		want   Outcome
	}{
		"could not start": {
			report: FailedReport(testCheckedAt, errors.New("no token")),
			want:   Failed,
		},
		"no season": {
			report: Report{CheckedAt: testCheckedAt},
			want:   Failed,
		},
		"primary authorized": {
			report: Report{Seasons: []SeasonResult{{Season: testSeason, Outcome: Authorized}}},
			want:   Authorized,
		},
		"primary not authorized": {
			report: Report{Seasons: []SeasonResult{{Season: testSeason, Outcome: NotAuthorized}}},
			want:   NotAuthorized,
		},
		"control failed does not change the verdict": {
			report: Report{Seasons: []SeasonResult{
				{Season: testSeason, Outcome: Authorized},
				{Season: testPreviousSeason, Outcome: Failed},
			}},
			want: Authorized,
		},
		"control not authorized does not change the verdict": {
			report: Report{Seasons: []SeasonResult{
				{Season: testSeason, Outcome: Authorized},
				{Season: testPreviousSeason, Outcome: NotAuthorized},
			}},
			want: Authorized,
		},
		"primary failed": {
			report: Report{Seasons: []SeasonResult{
				{Season: testSeason, Outcome: Failed},
				{Season: testPreviousSeason, Outcome: Authorized},
			}},
			want: Failed,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.report.Outcome())
		})
	}
}

func TestReport_Text(t *testing.T) {
	report := Report{
		CheckedAt: testCheckedAt,
		Seasons: []SeasonResult{
			{
				Season:  testSeason,
				Outcome: NotAuthorized,
				Probes: []Probe{
					{Name: "game key", URL: gameKeyURL, StatusCode: http.StatusForbidden, Detail: notAuthorized},
					{Name: "league 111 settings", Skipped: true, Detail: "not checked: game key unknown"},
				},
				LeaguesChecked: 1,
			},
			{
				Season:  testPreviousSeason,
				Outcome: Authorized,
				Probes: []Probe{
					{Name: "game key", URL: previousGameKeyURL, StatusCode: http.StatusOK},
				},
			},
		},
	}

	want := "Yahoo Fantasy API access check\n" +
		"Checked at 2026-10-01T12:00:00Z.\n\n" +
		"2026 season: NOT AUTHORIZED\n" +
		"- game key: 403 Forbidden - " + notAuthorized + "\n" +
		"  " + gameKeyURL + "\n" +
		"- league 111 settings: not checked: game key unknown\n\n" +
		"2025 season: AUTHORIZED\n" +
		"- game key: 200 OK\n" +
		"  " + previousGameKeyURL + "\n\n" +
		"Yahoo still refuses the app (403). Nothing to do until the next run.\n"
	require.Equal(t, want, report.Text())
}

func TestReport_TextOfFailedStart(t *testing.T) {
	text := FailedReport(testCheckedAt, errors.New("no oauth2 token found in redis")).Text()
	require.Contains(t, text, "The check could not run: no oauth2 token found in redis\n")
	require.Contains(t, text, "sign in again at /yahoo/login")
}
