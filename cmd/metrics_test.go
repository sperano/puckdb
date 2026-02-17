package cmd

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
)

func TestClassifyFileType(t *testing.T) {
	tests := []struct {
		name     string
		dataPath string
		filePath string
		filename string
		want     string
	}{
		{
			name:     "player landing page",
			dataPath: "/data",
			filePath: "/data/players/12345.json",
			filename: "12345.json",
			want:     store.FileTypePlayerLanding,
		},
		{
			name:     "yahoo player file",
			dataPath: "/data",
			filePath: "/data/yahoo-players/player-123.html",
			filename: "player-123.html",
			want:     store.FileTypeYahooPlayer,
		},
		{
			name:     "yahoo player missing file",
			dataPath: "/data",
			filePath: "/data/yahoo-players-missing/player-456.html",
			filename: "player-456.html",
			want:     store.FileTypeYahooPlayer,
		},
		{
			name:     "game key file",
			dataPath: "/data",
			filePath: "/data/game-keys/2024010001.json",
			filename: "2024010001.json",
			want:     store.FileTypeGameKey,
		},
		{
			name:     "boxscore file",
			dataPath: "/data",
			filePath: "/data/games/2024/boxscore-2024010001.json",
			filename: "boxscore-2024010001.json",
			want:     store.FileTypeBoxscore,
		},
		{
			name:     "playbyplay file",
			dataPath: "/data",
			filePath: "/data/games/2024/playbyplay-2024020001.json",
			filename: "playbyplay-2024020001.json",
			want:     store.FileTypePlayByPlay,
		},
		{
			name:     "shiftchart file",
			dataPath: "/data",
			filePath: "/data/games/2024/shiftchart-2024020001.json",
			filename: "shiftchart-2024020001.json",
			want:     store.FileTypeShiftChart,
		},
		{
			name:     "daily schedule file",
			dataPath: "/data",
			filePath: "/data/games/2024/daily-schedule-2024-10-15.json",
			filename: "daily-schedule-2024-10-15.json",
			want:     store.FileTypeDailySchedule,
		},
		{
			name:     "league file",
			dataPath: "/data",
			filePath: "/data/yahoo/2024/league-12345.json",
			filename: "league-12345.json",
			want:     store.FileTypeLeague,
		},
		{
			name:     "team file",
			dataPath: "/data",
			filePath: "/data/yahoo/2024/team-12345-1.json",
			filename: "team-12345-1.json",
			want:     store.FileTypeTeam,
		},
		{
			name:     "roster file",
			dataPath: "/data",
			filePath: "/data/yahoo/2024/rosters-2024-10-15.json",
			filename: "rosters-2024-10-15.json",
			want:     store.FileTypeRoster,
		},
		{
			name:     "team summary file",
			dataPath: "/data",
			filePath: "/data/yahoo/2024/team-summary-12345-1-2024-10-15.json",
			filename: "team-summary-12345-1-2024-10-15.json",
			want:     store.FileTypeTeamSummary,
		},
		{
			name:     "franchises file",
			dataPath: "/data",
			filePath: "/data/nhl/franchises.json",
			filename: "franchises",
			want:     store.FileTypeFranchises,
		},
		{
			name:     "seasons manifest file",
			dataPath: "/data",
			filePath: "/data/nhl/seasons-manifest.json",
			filename: "seasons-manifest",
			want:     store.FileTypeSeasonsManifest,
		},
		{
			name:     "season standings file",
			dataPath: "/data",
			filePath: "/data/nhl/standings/standings-20232024.json",
			filename: "standings-20232024",
			want:     store.FileTypeSeasonStandings,
		},
		{
			name:     "unknown file",
			dataPath: "/data",
			filePath: "/data/other/random.txt",
			filename: "random.txt",
			want:     store.FileTypeUnknown,
		},
		{
			name:     "file at root with unknown type",
			dataPath: "/data",
			filePath: "/data/config.json",
			filename: "config.json",
			want:     store.FileTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyFileType(tt.dataPath, tt.filePath, tt.filename)
			if got != tt.want {
				t.Errorf("classifyFileType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCountDays(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		want  int
	}{
		{
			name:  "same day",
			start: time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 15, 23, 59, 59, 0, time.UTC),
			want:  1,
		},
		{
			name:  "two consecutive days",
			start: time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 16, 0, 0, 0, 0, time.UTC),
			want:  2,
		},
		{
			name:  "full week",
			start: time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 7, 0, 0, 0, 0, time.UTC),
			want:  7,
		},
		{
			name:  "month span",
			start: time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 31, 0, 0, 0, 0, time.UTC),
			want:  31,
		},
		{
			name:  "cross year boundary",
			start: time.Date(2024, 12, 30, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			want:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countDays(tt.start, tt.end)
			if got != tt.want {
				t.Errorf("countDays() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFilterRegularSeasonGames(t *testing.T) {
	// Game ID format: YYYYTTNNNN where TT is game type
	// 01 = preseason, 02 = regular season, 03 = playoffs
	tests := []struct {
		name    string
		gameIDs []nhl.GameID
		want    int // expected count after filtering
	}{
		{
			name:    "empty list",
			gameIDs: []nhl.GameID{},
			want:    0,
		},
		{
			name: "all regular season",
			gameIDs: []nhl.GameID{
				nhl.GameID(2024020001),
				nhl.GameID(2024020002),
				nhl.GameID(2024020003),
			},
			want: 3,
		},
		{
			name: "all preseason - filtered out",
			gameIDs: []nhl.GameID{
				nhl.GameID(2024010001),
				nhl.GameID(2024010002),
			},
			want: 0,
		},
		{
			name: "mixed preseason and regular",
			gameIDs: []nhl.GameID{
				nhl.GameID(2024010001), // preseason
				nhl.GameID(2024020001), // regular
				nhl.GameID(2024010002), // preseason
				nhl.GameID(2024020002), // regular
			},
			want: 2,
		},
		{
			name: "playoffs included",
			gameIDs: []nhl.GameID{
				nhl.GameID(2024020001), // regular
				nhl.GameID(2024030001), // playoffs
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterRegularSeasonGames(tt.gameIDs)
			if len(got) != tt.want {
				t.Errorf("filterRegularSeasonGames() returned %d games, want %d", len(got), tt.want)
			}
		})
	}
}

func TestRenderProgressBar(t *testing.T) {
	tests := []struct {
		name  string
		pct   float64
		width int
		want  string
	}{
		{
			name:  "0 percent",
			pct:   0,
			width: 10,
			want:  "[░░░░░░░░░░]",
		},
		{
			name:  "50 percent",
			pct:   50,
			width: 10,
			want:  "[█████░░░░░]",
		},
		{
			name:  "100 percent",
			pct:   100,
			width: 10,
			want:  "[██████████]",
		},
		{
			name:  "25 percent width 20",
			pct:   25,
			width: 20,
			want:  "[█████░░░░░░░░░░░░░░░]",
		},
		{
			name:  "negative clamped to 0",
			pct:   -10,
			width: 10,
			want:  "[░░░░░░░░░░]",
		},
		{
			name:  "over 100 clamped",
			pct:   150,
			width: 10,
			want:  "[██████████]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderProgressBar(tt.pct, tt.width)
			if got != tt.want {
				t.Errorf("renderProgressBar() = %q, want %q", got, tt.want)
			}
		})
	}
}
