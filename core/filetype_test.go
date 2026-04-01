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
		{"last type", YahooMatchups, "YahooMatchups"},
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
