package cmd

import (
	"strings"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
)

func TestClassifyFileType(t *testing.T) {
	tests := []struct {
		name     string
		dataPath string
		filePath string
		filename string
		want     core.FileType
	}{
		{
			name:     "player landing page",
			dataPath: "/data",
			filePath: "/data/players/12345.json",
			filename: "12345.json",
			want:     core.PlayerLanding,
		},
		{
			name:     "yahoo player file",
			dataPath: "/data",
			filePath: "/data/yahoo-players/player-123.html",
			filename: "player-123.html",
			want:     core.YahooPlayer,
		},
		{
			name:     "yahoo player missing file",
			dataPath: "/data",
			filePath: "/data/yahoo-players-missing/player-456.html",
			filename: "player-456.html",
			want:     core.YahooPlayer,
		},
		{
			name:     "game key file",
			dataPath: "/data",
			filePath: "/data/game-keys/2024010001.json",
			filename: "2024010001.json",
			want:     core.GameKey,
		},
		{
			name:     "boxscore file",
			dataPath: "/data",
			filePath: "/data/games/2024/boxscore-2024010001.json",
			filename: "boxscore-2024010001.json",
			want:     core.Boxscore,
		},
		{
			name:     "playbyplay file",
			dataPath: "/data",
			filePath: "/data/games/2024/playbyplay-2024020001.json",
			filename: "playbyplay-2024020001.json",
			want:     core.PlayByPlay,
		},
		{
			name:     "shiftchart file",
			dataPath: "/data",
			filePath: "/data/games/2024/shiftchart-2024020001.json",
			filename: "shiftchart-2024020001.json",
			want:     core.ShiftChart,
		},
		{
			name:     "daily schedule file",
			dataPath: "/data",
			filePath: "/data/games/2024/daily-schedule-2024-10-15.json",
			filename: "daily-schedule-2024-10-15.json",
			want:     core.DailySchedule,
		},
		{
			name:     "league file",
			dataPath: "/data",
			filePath: "/data/yahoo/2024/league-12345.json",
			filename: "league-12345.json",
			want:     core.League,
		},
		{
			name:     "team file",
			dataPath: "/data",
			filePath: "/data/yahoo/2024/team-12345-1.json",
			filename: "team-12345-1.json",
			want:     core.Team,
		},
		{
			name:     "roster file",
			dataPath: "/data",
			filePath: "/data/yahoo/2024/rosters-2024-10-15.json",
			filename: "rosters-2024-10-15.json",
			want:     core.Roster,
		},
		{
			name:     "team summary file",
			dataPath: "/data",
			filePath: "/data/yahoo/2024/team-summary-12345-1-2024-10-15.json",
			filename: "team-summary-12345-1-2024-10-15.json",
			want:     core.TeamSummary,
		},
		{
			name:     "franchises file",
			dataPath: "/data",
			filePath: "/data/nhl/franchises.json",
			filename: "franchises",
			want:     core.Franchises,
		},
		{
			name:     "seasons manifest file",
			dataPath: "/data",
			filePath: "/data/nhl/seasons-manifest.json",
			filename: "seasons-manifest",
			want:     core.SeasonsManifest,
		},
		{
			name:     "season standings file",
			dataPath: "/data",
			filePath: "/data/nhl/standings/standings-20232024.json",
			filename: "standings-20232024",
			want:     core.SeasonStandings,
		},
		{
			name:     "asset player headshot",
			dataPath: "/data",
			filePath: "/data/assets/players/8478402/headshot.png",
			filename: "headshot.png",
			want:     core.PlayerHeadshot,
		},
		{
			name:     "asset player hero image",
			dataPath: "/data",
			filePath: "/data/assets/players/8478402/hero.jpg",
			filename: "hero.jpg",
			want:     core.PlayerHeroImage,
		},
		{
			name:     "asset yahoo image",
			dataPath: "/data",
			filePath: "/data/assets/players/8478402/yahoo.png",
			filename: "yahoo.png",
			want:     core.PlayerYahooImage,
		},
		{
			name:     "asset team logo",
			dataPath: "/data",
			filePath: "/data/assets/teams/logos/10.svg",
			filename: "10.svg",
			want:     core.TeamLogo,
		},
		{
			name:     "asset yahoo league logo",
			dataPath: "/data",
			filePath: "/data/assets/yahoo/leagues/42/league-logo.png",
			filename: "league-logo.png",
			want:     core.YahooLeagueLogo,
		},
		{
			name:     "asset yahoo team logo",
			dataPath: "/data",
			filePath: "/data/assets/yahoo/leagues/42/teams/3/logo.png",
			filename: "logo.png",
			want:     core.YahooTeamLogo,
		},
		{
			name:     "asset yahoo manager image",
			dataPath: "/data",
			filePath: "/data/assets/yahoo/leagues/42/teams/3/manager-7.jpg",
			filename: "manager-7.jpg",
			want:     core.YahooManagerImage,
		},
		{
			name:     "asset unknown subdirectory falls through",
			dataPath: "/data",
			filePath: "/data/assets/garbage/foo.png",
			filename: "foo.png",
			want:     core.Unknown,
		},
		{
			name:     "unknown file",
			dataPath: "/data",
			filePath: "/data/other/random.txt",
			filename: "random.txt",
			want:     core.Unknown,
		},
		{
			name:     "file at root with unknown type",
			dataPath: "/data",
			filePath: "/data/config.json",
			filename: "config.json",
			want:     core.Unknown,
		},

		// Production paths — what disk files actually look like (vs the older
		// synthetic test paths above). These exercise classifySeasonFile,
		// classifyNHLFile, and classifyYahooSeasonFile rather than the
		// filename-prefix fallback.
		{
			name:     "franchises with .json extension (production)",
			dataPath: "/data",
			filePath: "/data/nhl/franchises.json",
			filename: "franchises.json",
			want:     core.Franchises,
		},
		{
			name:     "seasons-manifest with .json extension (production)",
			dataPath: "/data",
			filePath: "/data/nhl/seasons-manifest.json",
			filename: "seasons-manifest.json",
			want:     core.SeasonsManifest,
		},
		{
			name:     "season standings (production layout)",
			dataPath: "/data",
			filePath: "/data/nhl/standings/standings-20232024.json",
			filename: "standings-20232024.json",
			want:     core.SeasonStandings,
		},
		{
			name:     "boxscore in production seasons/games tree",
			dataPath: "/data",
			filePath: "/data/seasons/2023/games/2024/01/15/boxscore-2023020512.json",
			filename: "boxscore-2023020512.json",
			want:     core.Boxscore,
		},
		{
			name:     "season series in production seasons/games tree",
			dataPath: "/data",
			filePath: "/data/seasons/2023/games/2024/01/15/seasonseries-2023020512.json",
			filename: "seasonseries-2023020512.json",
			want:     core.SeasonSeries,
		},
		{
			name:     "daily standings under games (date-keyed)",
			dataPath: "/data",
			filePath: "/data/seasons/2023/games/2024/01/15/standings-2024-01-15.json",
			filename: "standings-2024-01-15.json",
			want:     core.DailyStandings,
		},
		{
			name:     "season roster (singular roster-)",
			dataPath: "/data",
			filePath: "/data/seasons/2023/rosters/roster-NYR.json",
			filename: "roster-NYR.json",
			want:     core.SeasonRoster,
		},
		{
			name:     "club stats per season",
			dataPath: "/data",
			filePath: "/data/seasons/2023/clubstats/clubstats-NYR-2.json",
			filename: "clubstats-NYR-2.json",
			want:     core.ClubStatsResource,
		},
		{
			name:     "club schedule per season",
			dataPath: "/data",
			filePath: "/data/seasons/2023/club-schedule/club-schedule-NYR.json",
			filename: "club-schedule-NYR.json",
			want:     core.ClubScheduleSeasonResource,
		},
		{
			name:     "player game log",
			dataPath: "/data",
			filePath: "/data/seasons/2023/player-gamelogs/player-8478402-2.json",
			filename: "player-8478402-2.json",
			want:     core.PlayerGameLog,
		},
		{
			name:     "missing player landing maps to PlayerLanding",
			dataPath: "/data",
			filePath: "/data/players-missing/player-8478402.json",
			filename: "player-8478402.json",
			want:     core.PlayerLanding,
		},
		{
			name:     "yahoo league file (production layout)",
			dataPath: "/data",
			filePath: "/data/seasons/2023/yahoo/12345/league/league.xml",
			filename: "league.xml",
			want:     core.League,
		},
		{
			name:     "yahoo team file (production layout)",
			dataPath: "/data",
			filePath: "/data/seasons/2023/yahoo/12345/teams/team-01/team-01.xml",
			filename: "team-01.xml",
			want:     core.Team,
		},
		{
			name:     "yahoo roster file (production layout)",
			dataPath: "/data",
			filePath: "/data/seasons/2023/yahoo/12345/rosters/team-01/rosters-01-2024-01-15.xml",
			filename: "rosters-01-2024-01-15.xml",
			want:     core.Roster,
		},
		{
			name:     "yahoo team summary (production layout)",
			dataPath: "/data",
			filePath: "/data/seasons/2023/yahoo/12345/summaries/team-01/team-01-summary-2024-01-15.xml",
			filename: "team-01-summary-2024-01-15.xml",
			want:     core.TeamSummary,
		},
		{
			name:     "yahoo transactions",
			dataPath: "/data",
			filePath: "/data/seasons/2023/yahoo/12345/transactions/transactions.xml",
			filename: "transactions.xml",
			want:     core.YahooTransactions,
		},
		{
			name:     "yahoo draft results",
			dataPath: "/data",
			filePath: "/data/seasons/2023/yahoo/12345/draft/draftresults.xml",
			filename: "draftresults.xml",
			want:     core.YahooDraftResults,
		},
		{
			name:     "yahoo weekly matchup",
			dataPath: "/data",
			filePath: "/data/seasons/2023/yahoo/12345/matchups/week-7.xml",
			filename: "week-7.xml",
			want:     core.YahooMatchups,
		},
		{
			name:     "yahoo league player pool page",
			dataPath: "/data",
			filePath: "/data/seasons/2026/yahoo/1001/players/1758369600000/players-0025.xml",
			filename: "players-0025.xml",
			want:     core.YahooLeaguePlayers,
		},
		{
			name:     "yahoo league player pool manifest is not a page",
			dataPath: "/data",
			filePath: "/data/seasons/2026/yahoo/1001/players/manifest.json",
			filename: "manifest.json",
			want:     core.Unknown,
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
			got := filterFinalNonPreseasonGames(tt.gameIDs)
			if len(got) != tt.want {
				t.Errorf("filterFinalNonPreseasonGames() returned %d games, want %d", len(got), tt.want)
			}
		})
	}
}

func TestRenderProgressBar(t *testing.T) {
	// Helper to build expected bar with shade sentinels.
	// Each segment: shade_marker + filled + shade_end + empty
	seg := func(shade, fill, empty int) string {
		return progressShades[shade] + strings.Repeat("█", fill) + ProgressShadeEnd + strings.Repeat("░", empty)
	}

	tests := []struct {
		name  string
		pct   float64
		width int
		want  string
	}{
		{
			name:  "0 percent",
			pct:   0,
			width: 16, // segment=2
			want: "[" + seg(0, 0, 2) + seg(1, 0, 2) + seg(2, 0, 2) + seg(3, 0, 2) +
				seg(4, 0, 2) + seg(5, 0, 2) + seg(6, 0, 2) + seg(7, 0, 2) + "]",
		},
		{
			name:  "50 percent",
			pct:   50,
			width: 16,
			want: "[" + seg(0, 2, 0) + seg(1, 2, 0) + seg(2, 2, 0) + seg(3, 2, 0) +
				seg(4, 0, 2) + seg(5, 0, 2) + seg(6, 0, 2) + seg(7, 0, 2) + "]",
		},
		{
			name:  "100 percent",
			pct:   100,
			width: 16,
			want: "[" + seg(0, 2, 0) + seg(1, 2, 0) + seg(2, 2, 0) + seg(3, 2, 0) +
				seg(4, 2, 0) + seg(5, 2, 0) + seg(6, 2, 0) + seg(7, 2, 0) + "]",
		},
		{
			name:  "25 percent width 24",
			pct:   25,
			width: 24, // segment=3, filled=6 → fills shades 0-1 fully
			want: "[" + seg(0, 3, 0) + seg(1, 3, 0) + seg(2, 0, 3) + seg(3, 0, 3) +
				seg(4, 0, 3) + seg(5, 0, 3) + seg(6, 0, 3) + seg(7, 0, 3) + "]",
		},
		{
			name:  "negative clamped to 0",
			pct:   -10,
			width: 16,
			want: "[" + seg(0, 0, 2) + seg(1, 0, 2) + seg(2, 0, 2) + seg(3, 0, 2) +
				seg(4, 0, 2) + seg(5, 0, 2) + seg(6, 0, 2) + seg(7, 0, 2) + "]",
		},
		{
			name:  "over 100 clamped",
			pct:   150,
			width: 16,
			want: "[" + seg(0, 2, 0) + seg(1, 2, 0) + seg(2, 2, 0) + seg(3, 2, 0) +
				seg(4, 2, 0) + seg(5, 2, 0) + seg(6, 2, 0) + seg(7, 2, 0) + "]",
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
