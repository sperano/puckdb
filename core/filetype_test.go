package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

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
		{"out-of-range positive", FileType(len(fileTypes)), "Unknown"},
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
	{PlayerYahooImage, "PlayerYahooImage"},
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

	if got, want := len(allFileTypeNames), len(fileTypes); got != want {
		t.Fatalf("allFileTypeNames has %d entries but core has %d fileTypes values; "+
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

// TestFileTypes_NoDriftFromIotaBlock parses filetype.go's AST and asserts the
// number of FileType const declarations matches the number of entries in the
// fileTypes table. Drift means: a constant was added to the iota block but not
// to the fileTypes table — its String() would silently return "Unknown" and
// it would never appear in AllFileTypes. The test stays in this package
// because fileTypes is unexported.
func TestFileTypes_NoDriftFromIotaBlock(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "filetype.go", nil, 0)
	if err != nil {
		t.Fatalf("parse filetype.go: %v", err)
	}

	var declared []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		// Only consider const blocks where the first spec carries an
		// explicit FileType type — that's the iota block.
		if len(gd.Specs) == 0 {
			continue
		}
		first, ok := gd.Specs[0].(*ast.ValueSpec)
		if !ok || first.Type == nil {
			continue
		}
		ident, ok := first.Type.(*ast.Ident)
		if !ok || ident.Name != "FileType" {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for _, name := range vs.Names {
				declared = append(declared, name.Name)
			}
		}
	}

	if len(declared) != len(fileTypes) {
		t.Fatalf("filetype.go declares %d FileType constants but the fileTypes "+
			"table has %d entries; add the new constant to fileTypes "+
			"(declarations: %v)", len(declared), len(fileTypes), declared)
	}
}

// TestFileTypes_NoDuplicates verifies the fileTypes table has unique FileType
// values and unique names. A duplicate FileType silently overwrites the
// nameByType map; a duplicate name silently overwrites typeByName, breaking
// ParseFileType for the loser.
func TestFileTypes_NoDuplicates(t *testing.T) {
	t.Parallel()

	seenFT := make(map[FileType]string, len(fileTypes))
	seenName := make(map[string]FileType, len(fileTypes))
	for _, e := range fileTypes {
		if prev, dup := seenFT[e.ft]; dup {
			t.Errorf("FileType(%d) appears twice in fileTypes (names %q and %q)",
				int(e.ft), prev, e.name)
		}
		seenFT[e.ft] = e.name
		if prev, dup := seenName[e.name]; dup {
			t.Errorf("name %q appears twice in fileTypes (FileTypes %d and %d)",
				e.name, int(prev), int(e.ft))
		}
		seenName[e.name] = e.ft
	}
}
