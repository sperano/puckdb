package core

import "testing"

type stubResource struct {
	path     string
	fileType FileType
}

func (s stubResource) Path() string  { return s.path }
func (s stubResource) Type() FileType { return s.fileType }

func TestRedisKey(t *testing.T) {
	t.Parallel()
	r := stubResource{path: "seasons/2024/games/2025/01/15/boxscore-2024020123.json"}
	got := RedisKey(r)
	want := RedisResourceKeyPrefix + "seasons/2024/games/2025/01/15/boxscore-2024020123.json"
	if got != want {
		t.Errorf("RedisKey() = %q, want %q", got, want)
	}
}

func TestParseFileType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantType  FileType
		wantFound bool
	}{
		{
			name:      "Unknown",
			input:     "Unknown",
			wantType:  Unknown,
			wantFound: true,
		},
		{
			name:      "Boxscore",
			input:     "Boxscore",
			wantType:  Boxscore,
			wantFound: true,
		},
		{
			name:      "SeasonSeries",
			input:     "SeasonSeries",
			wantType:  SeasonSeries,
			wantFound: true,
		},
		{
			name:      "DailyStandings",
			input:     "DailyStandings",
			wantType:  DailyStandings,
			wantFound: true,
		},
		{
			name:      "SeasonRoster",
			input:     "SeasonRoster",
			wantType:  SeasonRoster,
			wantFound: true,
		},
		{
			name:      "ClubStatsResource",
			input:     "ClubStatsResource",
			wantType:  ClubStatsResource,
			wantFound: true,
		},
		{
			name:      "YahooTransactions",
			input:     "YahooTransactions",
			wantType:  YahooTransactions,
			wantFound: true,
		},
		{
			name:      "YahooDraftResults",
			input:     "YahooDraftResults",
			wantType:  YahooDraftResults,
			wantFound: true,
		},
		{
			name:      "YahooMatchups",
			input:     "YahooMatchups",
			wantType:  YahooMatchups,
			wantFound: true,
		},
		{
			name:      "unrecognized name",
			input:     "NotARealType",
			wantType:  Unknown,
			wantFound: false,
		},
		{
			name:      "empty string",
			input:     "",
			wantType:  Unknown,
			wantFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := ParseFileType(tc.input)
			if found != tc.wantFound {
				t.Errorf("ParseFileType(%q) found = %v, want %v", tc.input, found, tc.wantFound)
			}
			if got != tc.wantType {
				t.Errorf("ParseFileType(%q) = %v, want %v", tc.input, got, tc.wantType)
			}
		})
	}
}

func TestFileType_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ft   FileType
		want string
	}{
		{"valid type", Boxscore, "Boxscore"},
		{"last type", ClubScheduleSeasonResource, "ClubScheduleSeason"},
		{"negative value", FileType(-1), "Unknown"},
		{"out-of-range positive", FileType(len(names)), "Unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.ft.String(); got != tc.want {
				t.Errorf("FileType(%d).String() = %q, want %q", int(tc.ft), got, tc.want)
			}
		})
	}
}

// allFileTypeNames lists every FileType constant with its expected String()
// output. New constants must be added here so the exhaustive tests below cover
// them.
//
// Note on ClubScheduleSeasonResource: by convention every constant Foo has
// String() == "Foo", but ClubScheduleSeasonResource → "ClubScheduleSeason"
// (no "Resource" suffix). The string is a Prometheus metric label value, so
// changing it could break existing Grafana queries — preserved as-is and
// pinned by the test.
var allFileTypeNames = []struct {
	ft   FileType
	want string
}{
	{Unknown, "Unknown"},
	{DailySchedule, "DailySchedule"},
	{Boxscore, "Boxscore"},
	{PlayByPlay, "PlayByPlay"},
	{ShiftChart, "ShiftChart"},
	{GameStory, "GameStory"},
	{Franchises, "Franchises"},
	{SeasonsManifest, "SeasonsManifest"},
	{SeasonStandings, "SeasonStandings"},
	{PlayerLanding, "PlayerLanding"},
	{PlayerGameLog, "PlayerGameLog"},
	{League, "League"},
	{Team, "Team"},
	{Roster, "Roster"},
	{TeamSummary, "TeamSummary"},
	{YahooPlayer, "YahooPlayer"},
	{GameKey, "GameKey"},
	{SeasonSeries, "SeasonSeries"},
	{DailyStandings, "DailyStandings"},
	{SeasonRoster, "SeasonRoster"},
	{ClubStatsResource, "ClubStatsResource"},
	{YahooTransactions, "YahooTransactions"},
	{YahooDraftResults, "YahooDraftResults"},
	{YahooMatchups, "YahooMatchups"},
	{EdgeSkaterDetail, "EdgeSkaterDetail"},
	{EdgeSkaterSpeedDetail, "EdgeSkaterSpeedDetail"},
	{EdgeSkaterDistanceDetail, "EdgeSkaterDistanceDetail"},
	{EdgeSkaterShotSpeedDetail, "EdgeSkaterShotSpeedDetail"},
	{EdgeSkaterShotLocationDetail, "EdgeSkaterShotLocationDetail"},
	{EdgeSkaterZoneTime, "EdgeSkaterZoneTime"},
	{EdgeSkaterComparison, "EdgeSkaterComparison"},
	{EdgeGoalieDetail, "EdgeGoalieDetail"},
	{EdgeGoalie5v5Detail, "EdgeGoalie5v5Detail"},
	{EdgeGoalieShotLocationDetail, "EdgeGoalieShotLocationDetail"},
	{EdgeGoalieSavePctgDetail, "EdgeGoalieSavePctgDetail"},
	{EdgeGoalieComparison, "EdgeGoalieComparison"},
	{EdgeTeamDetail, "EdgeTeamDetail"},
	{EdgeTeamSpeedDetail, "EdgeTeamSpeedDetail"},
	{EdgeTeamDistanceDetail, "EdgeTeamDistanceDetail"},
	{EdgeTeamShotSpeedDetail, "EdgeTeamShotSpeedDetail"},
	{EdgeTeamShotLocationDetail, "EdgeTeamShotLocationDetail"},
	{EdgeTeamZoneTimeDetails, "EdgeTeamZoneTimeDetails"},
	{EdgeTeamComparison, "EdgeTeamComparison"},
	{EdgeSkaterLanding, "EdgeSkaterLanding"},
	{EdgeGoalieLanding, "EdgeGoalieLanding"},
	{EdgeTeamLanding, "EdgeTeamLanding"},
	{ClubScheduleSeasonResource, "ClubScheduleSeason"},
	{PlayerHeadshot, "PlayerHeadshot"},
	{PlayerHeroImage, "PlayerHeroImage"},
	{PlayerYahooImageSmall, "PlayerYahooImageSmall"},
	{PlayerYahooImageMedium, "PlayerYahooImageMedium"},
	{PlayerYahooImageLarge, "PlayerYahooImageLarge"},
	{TeamLogo, "TeamLogo"},
	{YahooTeamLogo, "YahooTeamLogo"},
	{YahooLeagueLogo, "YahooLeagueLogo"},
	{YahooManagerImage, "YahooManagerImage"},
}

// TestFileType_String_AllConstants pins the String() output for every FileType
// constant. Failures here mean a constant was renamed, removed, or had its
// names-map entry edited — confirm callers (especially Prometheus dashboards)
// before updating expectations.
func TestFileType_String_AllConstants(t *testing.T) {
	t.Parallel()

	if got, want := len(allFileTypeNames), len(names); got != want {
		t.Fatalf("allFileTypeNames has %d entries but core has %d FileType values; "+
			"add the new constant to allFileTypeNames", got, want)
	}

	for _, tc := range allFileTypeNames {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.ft.String(); got != tc.want {
				t.Errorf("FileType(%d).String() = %q, want %q", int(tc.ft), got, tc.want)
			}
		})
	}
}

// TestFileType_StringParseRoundTrip asserts ParseFileType is the inverse of
// String for every FileType constant.
func TestFileType_StringParseRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range allFileTypeNames {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			ft, found := ParseFileType(tc.ft.String())
			if !found {
				t.Fatalf("ParseFileType(%q) not found", tc.ft.String())
			}
			if ft != tc.ft {
				t.Errorf("ParseFileType(%q) = %v, want %v", tc.ft.String(), ft, tc.ft)
			}
		})
	}
}
