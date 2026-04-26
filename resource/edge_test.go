package resource_test

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
)

// Shared fixtures for Edge resource tests. The 2024-2025 regular season
// produces season.APIString() == "20242025" and gameType.Int() == 2, so the
// formatted suffix in every Path/URL is "20242025-2" / "20242025/2".
var (
	edgeTestPlayer   = nhl.PlayerID(8478402)
	edgeTestTeam     = nhl.TeamID(10)
	edgeTestSeason   = nhl.NewSeason(2024)
	edgeTestGameType = nhl.GameType(2)
)

func TestEdgeResource_Path(t *testing.T) {
	tests := []struct {
		name     string
		resource core.Resource
		expected string
	}{
		// Skater per-player
		{"EdgeSkaterDetail", resource.EdgeSkaterDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/skaters/8478402/detail-20242025-2.json"},
		{"EdgeSkaterSpeedDetail", resource.EdgeSkaterSpeedDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/skaters/8478402/speed-20242025-2.json"},
		{"EdgeSkaterDistanceDetail", resource.EdgeSkaterDistanceDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/skaters/8478402/distance-20242025-2.json"},
		{"EdgeSkaterShotSpeedDetail", resource.EdgeSkaterShotSpeedDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/skaters/8478402/shot-speed-20242025-2.json"},
		{"EdgeSkaterShotLocationDetail", resource.EdgeSkaterShotLocationDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/skaters/8478402/shot-location-20242025-2.json"},
		{"EdgeSkaterZoneTime", resource.EdgeSkaterZoneTime{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/skaters/8478402/zone-time-20242025-2.json"},
		{"EdgeSkaterComparison", resource.EdgeSkaterComparison{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/skaters/8478402/comparison-20242025-2.json"},

		// Goalie per-player
		{"EdgeGoalieDetail", resource.EdgeGoalieDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/goalies/8478402/detail-20242025-2.json"},
		{"EdgeGoalie5v5Detail", resource.EdgeGoalie5v5Detail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/goalies/8478402/5v5-20242025-2.json"},
		{"EdgeGoalieShotLocationDetail", resource.EdgeGoalieShotLocationDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/goalies/8478402/shot-location-20242025-2.json"},
		{"EdgeGoalieSavePctgDetail", resource.EdgeGoalieSavePctgDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/goalies/8478402/save-pctg-20242025-2.json"},
		{"EdgeGoalieComparison", resource.EdgeGoalieComparison{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/goalies/8478402/comparison-20242025-2.json"},

		// Team per-team
		{"EdgeTeamDetail", resource.EdgeTeamDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/teams/10/detail-20242025-2.json"},
		{"EdgeTeamSpeedDetail", resource.EdgeTeamSpeedDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/teams/10/speed-20242025-2.json"},
		{"EdgeTeamDistanceDetail", resource.EdgeTeamDistanceDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/teams/10/distance-20242025-2.json"},
		{"EdgeTeamShotSpeedDetail", resource.EdgeTeamShotSpeedDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/teams/10/shot-speed-20242025-2.json"},
		{"EdgeTeamShotLocationDetail", resource.EdgeTeamShotLocationDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/teams/10/shot-location-20242025-2.json"},
		{"EdgeTeamZoneTimeDetails", resource.EdgeTeamZoneTimeDetails{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/teams/10/zone-time-details-20242025-2.json"},
		{"EdgeTeamComparison", resource.EdgeTeamComparison{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/teams/10/comparison-20242025-2.json"},

		// League-wide landings
		{"EdgeSkaterLanding", resource.EdgeSkaterLanding{Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/landing/skater-20242025-2.json"},
		{"EdgeGoalieLanding", resource.EdgeGoalieLanding{Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/landing/goalie-20242025-2.json"},
		{"EdgeTeamLanding", resource.EdgeTeamLanding{Season: edgeTestSeason, GameType: edgeTestGameType},
			"edge/landing/team-20242025-2.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resource.Path(); got != tt.expected {
				t.Errorf("%s.Path() = %q, want %q", tt.name, got, tt.expected)
			}
		})
	}
}

// urlProvider lets the URL test table hold heterogeneous concrete Edge
// resource types. core.Resource doesn't expose URL().
type urlProvider interface {
	URL() string
}

func TestEdgeResource_URL(t *testing.T) {
	const base = "https://api-web.nhle.com/v1"

	tests := []struct {
		name     string
		resource urlProvider
		expected string
	}{
		// Skater per-player
		{"EdgeSkaterDetail", resource.EdgeSkaterDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/skater-detail/8478402/20242025/2"},
		{"EdgeSkaterSpeedDetail", resource.EdgeSkaterSpeedDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/skater-skating-speed-detail/8478402/20242025/2"},
		{"EdgeSkaterDistanceDetail", resource.EdgeSkaterDistanceDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/skater-skating-distance-detail/8478402/20242025/2"},
		{"EdgeSkaterShotSpeedDetail", resource.EdgeSkaterShotSpeedDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/skater-shot-speed-detail/8478402/20242025/2"},
		{"EdgeSkaterShotLocationDetail", resource.EdgeSkaterShotLocationDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/skater-shot-location-detail/8478402/20242025/2"},
		{"EdgeSkaterZoneTime", resource.EdgeSkaterZoneTime{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/skater-zone-time/8478402/20242025/2"},
		{"EdgeSkaterComparison", resource.EdgeSkaterComparison{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/skater-comparison/8478402/20242025/2"},

		// Goalie per-player
		{"EdgeGoalieDetail", resource.EdgeGoalieDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/goalie-detail/8478402/20242025/2"},
		{"EdgeGoalie5v5Detail", resource.EdgeGoalie5v5Detail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/goalie-5v5-detail/8478402/20242025/2"},
		{"EdgeGoalieShotLocationDetail", resource.EdgeGoalieShotLocationDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/goalie-shot-location-detail/8478402/20242025/2"},
		{"EdgeGoalieSavePctgDetail", resource.EdgeGoalieSavePctgDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/goalie-save-percentage-detail/8478402/20242025/2"},
		{"EdgeGoalieComparison", resource.EdgeGoalieComparison{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/goalie-comparison/8478402/20242025/2"},

		// Team per-team
		{"EdgeTeamDetail", resource.EdgeTeamDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/team-detail/10/20242025/2"},
		{"EdgeTeamSpeedDetail", resource.EdgeTeamSpeedDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/team-skating-speed-detail/10/20242025/2"},
		{"EdgeTeamDistanceDetail", resource.EdgeTeamDistanceDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/team-skating-distance-detail/10/20242025/2"},
		{"EdgeTeamShotSpeedDetail", resource.EdgeTeamShotSpeedDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/team-shot-speed-detail/10/20242025/2"},
		{"EdgeTeamShotLocationDetail", resource.EdgeTeamShotLocationDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/team-shot-location-detail/10/20242025/2"},
		{"EdgeTeamZoneTimeDetails", resource.EdgeTeamZoneTimeDetails{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/team-zone-time-details/10/20242025/2"},
		{"EdgeTeamComparison", resource.EdgeTeamComparison{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/team-comparison/10/20242025/2"},

		// League-wide landings
		{"EdgeSkaterLanding", resource.EdgeSkaterLanding{Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/skater-landing/20242025/2"},
		{"EdgeGoalieLanding", resource.EdgeGoalieLanding{Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/goalie-landing/20242025/2"},
		{"EdgeTeamLanding", resource.EdgeTeamLanding{Season: edgeTestSeason, GameType: edgeTestGameType},
			base + "/edge/team-landing/20242025/2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resource.URL(); got != tt.expected {
				t.Errorf("%s.URL() = %q, want %q", tt.name, got, tt.expected)
			}
		})
	}
}

func TestEdgeResource_Type(t *testing.T) {
	tests := []struct {
		name     string
		resource core.Resource
		expected core.FileType
	}{
		{"EdgeSkaterDetail", resource.EdgeSkaterDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeSkaterDetail},
		{"EdgeSkaterSpeedDetail", resource.EdgeSkaterSpeedDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeSkaterSpeedDetail},
		{"EdgeSkaterDistanceDetail", resource.EdgeSkaterDistanceDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeSkaterDistanceDetail},
		{"EdgeSkaterShotSpeedDetail", resource.EdgeSkaterShotSpeedDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeSkaterShotSpeedDetail},
		{"EdgeSkaterShotLocationDetail", resource.EdgeSkaterShotLocationDetail{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeSkaterShotLocationDetail},
		{"EdgeSkaterZoneTime", resource.EdgeSkaterZoneTime{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeSkaterZoneTime},
		{"EdgeSkaterComparison", resource.EdgeSkaterComparison{PlayerID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeSkaterComparison},

		{"EdgeGoalieDetail", resource.EdgeGoalieDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeGoalieDetail},
		{"EdgeGoalie5v5Detail", resource.EdgeGoalie5v5Detail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeGoalie5v5Detail},
		{"EdgeGoalieShotLocationDetail", resource.EdgeGoalieShotLocationDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeGoalieShotLocationDetail},
		{"EdgeGoalieSavePctgDetail", resource.EdgeGoalieSavePctgDetail{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeGoalieSavePctgDetail},
		{"EdgeGoalieComparison", resource.EdgeGoalieComparison{GoalieID: edgeTestPlayer, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeGoalieComparison},

		{"EdgeTeamDetail", resource.EdgeTeamDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeTeamDetail},
		{"EdgeTeamSpeedDetail", resource.EdgeTeamSpeedDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeTeamSpeedDetail},
		{"EdgeTeamDistanceDetail", resource.EdgeTeamDistanceDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeTeamDistanceDetail},
		{"EdgeTeamShotSpeedDetail", resource.EdgeTeamShotSpeedDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeTeamShotSpeedDetail},
		{"EdgeTeamShotLocationDetail", resource.EdgeTeamShotLocationDetail{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeTeamShotLocationDetail},
		{"EdgeTeamZoneTimeDetails", resource.EdgeTeamZoneTimeDetails{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeTeamZoneTimeDetails},
		{"EdgeTeamComparison", resource.EdgeTeamComparison{TeamID: edgeTestTeam, Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeTeamComparison},

		{"EdgeSkaterLanding", resource.EdgeSkaterLanding{Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeSkaterLanding},
		{"EdgeGoalieLanding", resource.EdgeGoalieLanding{Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeGoalieLanding},
		{"EdgeTeamLanding", resource.EdgeTeamLanding{Season: edgeTestSeason, GameType: edgeTestGameType}, core.EdgeTeamLanding},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resource.Type(); got != tt.expected {
				t.Errorf("%s.Type() = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}
