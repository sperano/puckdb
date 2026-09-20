package resource

import (
	"encoding/json"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
)

// edgeSeasonGameType formats season/gameType for Edge storage paths and URLs.
func edgeSeasonGameType(season nhl.Season, gameType nhl.GameType) string {
	return fmt.Sprintf("%s-%d", season.APIString(), gameType.Int())
}

// ===== Edge Skater Resources =====

// EdgeSkaterDetail represents a skater's combined Edge stats.
type EdgeSkaterDetail struct {
	PlayerID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeSkaterDetail) Path() string {
	return fmt.Sprintf("edge/skaters/%s/detail-%s.json", r.PlayerID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeSkaterDetail) URL() string {
	return fmt.Sprintf("%s/edge/skater-detail/%s/%s/%d", baseURLAPIWebV1, r.PlayerID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeSkaterDetail) Type() core.FileType { return core.EdgeSkaterDetail }

func (r EdgeSkaterDetail) Parse(data []byte) (*nhl.EdgeSkaterDetail, error) {
	var v nhl.EdgeSkaterDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge skater detail %s: %w", r.PlayerID, err)
	}
	return &v, nil
}

func (r EdgeSkaterDetail) Format(obj *nhl.EdgeSkaterDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeSkaterSpeedDetail represents a skater's per-game top skating speeds.
type EdgeSkaterSpeedDetail struct {
	PlayerID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeSkaterSpeedDetail) Path() string {
	return fmt.Sprintf("edge/skaters/%s/speed-%s.json", r.PlayerID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeSkaterSpeedDetail) URL() string {
	return fmt.Sprintf("%s/edge/skater-skating-speed-detail/%s/%s/%d", baseURLAPIWebV1, r.PlayerID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeSkaterSpeedDetail) Type() core.FileType { return core.EdgeSkaterSpeedDetail }

func (r EdgeSkaterSpeedDetail) Parse(data []byte) (*nhl.EdgeSkaterSpeedDetail, error) {
	var v nhl.EdgeSkaterSpeedDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge skater speed detail %s: %w", r.PlayerID, err)
	}
	return &v, nil
}

func (r EdgeSkaterSpeedDetail) Format(obj *nhl.EdgeSkaterSpeedDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeSkaterDistanceDetail represents a skater's per-game distance skated.
type EdgeSkaterDistanceDetail struct {
	PlayerID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeSkaterDistanceDetail) Path() string {
	return fmt.Sprintf("edge/skaters/%s/distance-%s.json", r.PlayerID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeSkaterDistanceDetail) URL() string {
	return fmt.Sprintf("%s/edge/skater-skating-distance-detail/%s/%s/%d", baseURLAPIWebV1, r.PlayerID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeSkaterDistanceDetail) Type() core.FileType { return core.EdgeSkaterDistanceDetail }

func (r EdgeSkaterDistanceDetail) Parse(data []byte) (*nhl.EdgeSkaterDistanceDetail, error) {
	var v nhl.EdgeSkaterDistanceDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge skater distance detail %s: %w", r.PlayerID, err)
	}
	return &v, nil
}

func (r EdgeSkaterDistanceDetail) Format(obj *nhl.EdgeSkaterDistanceDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeSkaterShotSpeedDetail represents a skater's per-game hardest shots.
type EdgeSkaterShotSpeedDetail struct {
	PlayerID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeSkaterShotSpeedDetail) Path() string {
	return fmt.Sprintf("edge/skaters/%s/shot-speed-%s.json", r.PlayerID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeSkaterShotSpeedDetail) URL() string {
	return fmt.Sprintf("%s/edge/skater-shot-speed-detail/%s/%s/%d", baseURLAPIWebV1, r.PlayerID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeSkaterShotSpeedDetail) Type() core.FileType { return core.EdgeSkaterShotSpeedDetail }

func (r EdgeSkaterShotSpeedDetail) Parse(data []byte) (*nhl.EdgeSkaterShotSpeedDetail, error) {
	var v nhl.EdgeSkaterShotSpeedDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge skater shot speed detail %s: %w", r.PlayerID, err)
	}
	return &v, nil
}

func (r EdgeSkaterShotSpeedDetail) Format(obj *nhl.EdgeSkaterShotSpeedDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeSkaterShotLocationDetail represents a skater's shot location breakdown.
type EdgeSkaterShotLocationDetail struct {
	PlayerID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeSkaterShotLocationDetail) Path() string {
	return fmt.Sprintf("edge/skaters/%s/shot-location-%s.json", r.PlayerID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeSkaterShotLocationDetail) URL() string {
	return fmt.Sprintf("%s/edge/skater-shot-location-detail/%s/%s/%d", baseURLAPIWebV1, r.PlayerID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeSkaterShotLocationDetail) Type() core.FileType { return core.EdgeSkaterShotLocationDetail }

func (r EdgeSkaterShotLocationDetail) Parse(data []byte) (*nhl.EdgeSkaterShotLocationDetail, error) {
	var v nhl.EdgeSkaterShotLocationDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge skater shot location detail %s: %w", r.PlayerID, err)
	}
	return &v, nil
}

func (r EdgeSkaterShotLocationDetail) Format(obj *nhl.EdgeSkaterShotLocationDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeSkaterZoneTime represents a skater's zone time breakdown.
type EdgeSkaterZoneTime struct {
	PlayerID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeSkaterZoneTime) Path() string {
	return fmt.Sprintf("edge/skaters/%s/zone-time-%s.json", r.PlayerID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeSkaterZoneTime) URL() string {
	return fmt.Sprintf("%s/edge/skater-zone-time/%s/%s/%d", baseURLAPIWebV1, r.PlayerID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeSkaterZoneTime) Type() core.FileType { return core.EdgeSkaterZoneTime }

func (r EdgeSkaterZoneTime) Parse(data []byte) (*nhl.EdgeSkaterZoneTimeDetail, error) {
	var v nhl.EdgeSkaterZoneTimeDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge skater zone time %s: %w", r.PlayerID, err)
	}
	return &v, nil
}

func (r EdgeSkaterZoneTime) Format(obj *nhl.EdgeSkaterZoneTimeDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeSkaterComparison represents a skater's composite comparison data.
type EdgeSkaterComparison struct {
	PlayerID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeSkaterComparison) Path() string {
	return fmt.Sprintf("edge/skaters/%s/comparison-%s.json", r.PlayerID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeSkaterComparison) URL() string {
	return fmt.Sprintf("%s/edge/skater-comparison/%s/%s/%d", baseURLAPIWebV1, r.PlayerID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeSkaterComparison) Type() core.FileType { return core.EdgeSkaterComparison }

func (r EdgeSkaterComparison) Parse(data []byte) (*nhl.EdgeSkaterComparison, error) {
	var v nhl.EdgeSkaterComparison
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge skater comparison %s: %w", r.PlayerID, err)
	}
	return &v, nil
}

func (r EdgeSkaterComparison) Format(obj *nhl.EdgeSkaterComparison) ([]byte, error) {
	return json.Marshal(obj)
}

// ===== Edge Goalie Resources =====

// EdgeGoalieDetail represents a goalie's combined Edge stats.
type EdgeGoalieDetail struct {
	GoalieID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeGoalieDetail) Path() string {
	return fmt.Sprintf("edge/goalies/%s/detail-%s.json", r.GoalieID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeGoalieDetail) URL() string {
	return fmt.Sprintf("%s/edge/goalie-detail/%s/%s/%d", baseURLAPIWebV1, r.GoalieID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeGoalieDetail) Type() core.FileType { return core.EdgeGoalieDetail }

func (r EdgeGoalieDetail) Parse(data []byte) (*nhl.EdgeGoalieDetail, error) {
	var v nhl.EdgeGoalieDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge goalie detail %s: %w", r.GoalieID, err)
	}
	return &v, nil
}

func (r EdgeGoalieDetail) Format(obj *nhl.EdgeGoalieDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeGoalie5v5Detail represents a goalie's per-game 5v5 save percentage.
type EdgeGoalie5v5Detail struct {
	GoalieID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeGoalie5v5Detail) Path() string {
	return fmt.Sprintf("edge/goalies/%s/5v5-%s.json", r.GoalieID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeGoalie5v5Detail) URL() string {
	return fmt.Sprintf("%s/edge/goalie-5v5-detail/%s/%s/%d", baseURLAPIWebV1, r.GoalieID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeGoalie5v5Detail) Type() core.FileType { return core.EdgeGoalie5v5Detail }

func (r EdgeGoalie5v5Detail) Parse(data []byte) (*nhl.EdgeGoalie5v5Detail, error) {
	var v nhl.EdgeGoalie5v5Detail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge goalie 5v5 detail %s: %w", r.GoalieID, err)
	}
	return &v, nil
}

func (r EdgeGoalie5v5Detail) Format(obj *nhl.EdgeGoalie5v5Detail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeGoalieShotLocationDetail represents a goalie's shot location breakdown.
type EdgeGoalieShotLocationDetail struct {
	GoalieID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeGoalieShotLocationDetail) Path() string {
	return fmt.Sprintf("edge/goalies/%s/shot-location-%s.json", r.GoalieID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeGoalieShotLocationDetail) URL() string {
	return fmt.Sprintf("%s/edge/goalie-shot-location-detail/%s/%s/%d", baseURLAPIWebV1, r.GoalieID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeGoalieShotLocationDetail) Type() core.FileType { return core.EdgeGoalieShotLocationDetail }

func (r EdgeGoalieShotLocationDetail) Parse(data []byte) (*nhl.EdgeGoalieShotLocationDetail, error) {
	var v nhl.EdgeGoalieShotLocationDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge goalie shot location detail %s: %w", r.GoalieID, err)
	}
	return &v, nil
}

func (r EdgeGoalieShotLocationDetail) Format(obj *nhl.EdgeGoalieShotLocationDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeGoalieSavePctgDetail represents a goalie's per-game save percentage.
type EdgeGoalieSavePctgDetail struct {
	GoalieID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeGoalieSavePctgDetail) Path() string {
	return fmt.Sprintf("edge/goalies/%s/save-pctg-%s.json", r.GoalieID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeGoalieSavePctgDetail) URL() string {
	return fmt.Sprintf("%s/edge/goalie-save-percentage-detail/%s/%s/%d", baseURLAPIWebV1, r.GoalieID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeGoalieSavePctgDetail) Type() core.FileType { return core.EdgeGoalieSavePctgDetail }

func (r EdgeGoalieSavePctgDetail) Parse(data []byte) (*nhl.EdgeGoalieSavePctgDetail, error) {
	var v nhl.EdgeGoalieSavePctgDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge goalie save pctg detail %s: %w", r.GoalieID, err)
	}
	return &v, nil
}

func (r EdgeGoalieSavePctgDetail) Format(obj *nhl.EdgeGoalieSavePctgDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeGoalieComparison represents a goalie's composite comparison data.
type EdgeGoalieComparison struct {
	GoalieID nhl.PlayerID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeGoalieComparison) Path() string {
	return fmt.Sprintf("edge/goalies/%s/comparison-%s.json", r.GoalieID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeGoalieComparison) URL() string {
	return fmt.Sprintf("%s/edge/goalie-comparison/%s/%s/%d", baseURLAPIWebV1, r.GoalieID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeGoalieComparison) Type() core.FileType { return core.EdgeGoalieComparison }

func (r EdgeGoalieComparison) Parse(data []byte) (*nhl.EdgeGoalieComparison, error) {
	var v nhl.EdgeGoalieComparison
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge goalie comparison %s: %w", r.GoalieID, err)
	}
	return &v, nil
}

func (r EdgeGoalieComparison) Format(obj *nhl.EdgeGoalieComparison) ([]byte, error) {
	return json.Marshal(obj)
}

// ===== Edge Team Resources =====

// EdgeTeamDetail represents a team's combined Edge stats.
type EdgeTeamDetail struct {
	TeamID   nhl.TeamID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeTeamDetail) Path() string {
	return fmt.Sprintf("edge/teams/%s/detail-%s.json", r.TeamID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeTeamDetail) URL() string {
	return fmt.Sprintf("%s/edge/team-detail/%s/%s/%d", baseURLAPIWebV1, r.TeamID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeTeamDetail) Type() core.FileType { return core.EdgeTeamDetail }

func (r EdgeTeamDetail) Parse(data []byte) (*nhl.EdgeTeamDetail, error) {
	var v nhl.EdgeTeamDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge team detail %s: %w", r.TeamID, err)
	}
	return &v, nil
}

func (r EdgeTeamDetail) Format(obj *nhl.EdgeTeamDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeTeamSpeedDetail represents a team's per-player top skating speeds.
type EdgeTeamSpeedDetail struct {
	TeamID   nhl.TeamID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeTeamSpeedDetail) Path() string {
	return fmt.Sprintf("edge/teams/%s/speed-%s.json", r.TeamID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeTeamSpeedDetail) URL() string {
	return fmt.Sprintf("%s/edge/team-skating-speed-detail/%s/%s/%d", baseURLAPIWebV1, r.TeamID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeTeamSpeedDetail) Type() core.FileType { return core.EdgeTeamSpeedDetail }

func (r EdgeTeamSpeedDetail) Parse(data []byte) (*nhl.EdgeTeamSpeedDetail, error) {
	var v nhl.EdgeTeamSpeedDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge team speed detail %s: %w", r.TeamID, err)
	}
	return &v, nil
}

func (r EdgeTeamSpeedDetail) Format(obj *nhl.EdgeTeamSpeedDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeTeamDistanceDetail represents a team's per-game distance skated.
type EdgeTeamDistanceDetail struct {
	TeamID   nhl.TeamID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeTeamDistanceDetail) Path() string {
	return fmt.Sprintf("edge/teams/%s/distance-%s.json", r.TeamID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeTeamDistanceDetail) URL() string {
	return fmt.Sprintf("%s/edge/team-skating-distance-detail/%s/%s/%d", baseURLAPIWebV1, r.TeamID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeTeamDistanceDetail) Type() core.FileType { return core.EdgeTeamDistanceDetail }

func (r EdgeTeamDistanceDetail) Parse(data []byte) (*nhl.EdgeTeamDistanceDetail, error) {
	var v nhl.EdgeTeamDistanceDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge team distance detail %s: %w", r.TeamID, err)
	}
	return &v, nil
}

func (r EdgeTeamDistanceDetail) Format(obj *nhl.EdgeTeamDistanceDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeTeamShotSpeedDetail represents a team's per-player hardest shots.
type EdgeTeamShotSpeedDetail struct {
	TeamID   nhl.TeamID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeTeamShotSpeedDetail) Path() string {
	return fmt.Sprintf("edge/teams/%s/shot-speed-%s.json", r.TeamID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeTeamShotSpeedDetail) URL() string {
	return fmt.Sprintf("%s/edge/team-shot-speed-detail/%s/%s/%d", baseURLAPIWebV1, r.TeamID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeTeamShotSpeedDetail) Type() core.FileType { return core.EdgeTeamShotSpeedDetail }

func (r EdgeTeamShotSpeedDetail) Parse(data []byte) (*nhl.EdgeTeamShotSpeedDetail, error) {
	var v nhl.EdgeTeamShotSpeedDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge team shot speed detail %s: %w", r.TeamID, err)
	}
	return &v, nil
}

func (r EdgeTeamShotSpeedDetail) Format(obj *nhl.EdgeTeamShotSpeedDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeTeamShotLocationDetail represents a team's shot location breakdown.
type EdgeTeamShotLocationDetail struct {
	TeamID   nhl.TeamID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeTeamShotLocationDetail) Path() string {
	return fmt.Sprintf("edge/teams/%s/shot-location-%s.json", r.TeamID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeTeamShotLocationDetail) URL() string {
	return fmt.Sprintf("%s/edge/team-shot-location-detail/%s/%s/%d", baseURLAPIWebV1, r.TeamID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeTeamShotLocationDetail) Type() core.FileType { return core.EdgeTeamShotLocationDetail }

func (r EdgeTeamShotLocationDetail) Parse(data []byte) (*nhl.EdgeTeamShotLocationDetail, error) {
	var v nhl.EdgeTeamShotLocationDetail
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge team shot location detail %s: %w", r.TeamID, err)
	}
	return &v, nil
}

func (r EdgeTeamShotLocationDetail) Format(obj *nhl.EdgeTeamShotLocationDetail) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeTeamZoneTimeDetails represents a team's zone time by strength code with shot differential.
type EdgeTeamZoneTimeDetails struct {
	TeamID   nhl.TeamID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeTeamZoneTimeDetails) Path() string {
	return fmt.Sprintf("edge/teams/%s/zone-time-details-%s.json", r.TeamID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeTeamZoneTimeDetails) URL() string {
	return fmt.Sprintf("%s/edge/team-zone-time-details/%s/%s/%d", baseURLAPIWebV1, r.TeamID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeTeamZoneTimeDetails) Type() core.FileType { return core.EdgeTeamZoneTimeDetails }

func (r EdgeTeamZoneTimeDetails) Parse(data []byte) (*nhl.EdgeTeamZoneTimeDetails, error) {
	var v nhl.EdgeTeamZoneTimeDetails
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge team zone time details %s: %w", r.TeamID, err)
	}
	return &v, nil
}

func (r EdgeTeamZoneTimeDetails) Format(obj *nhl.EdgeTeamZoneTimeDetails) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeTeamComparison represents a team's composite comparison data.
type EdgeTeamComparison struct {
	TeamID   nhl.TeamID
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeTeamComparison) Path() string {
	return fmt.Sprintf("edge/teams/%s/comparison-%s.json", r.TeamID.String(), edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeTeamComparison) URL() string {
	return fmt.Sprintf("%s/edge/team-comparison/%s/%s/%d", baseURLAPIWebV1, r.TeamID.String(), r.Season.APIString(), r.GameType.Int())
}

func (r EdgeTeamComparison) Type() core.FileType { return core.EdgeTeamComparison }

func (r EdgeTeamComparison) Parse(data []byte) (*nhl.EdgeTeamComparison, error) {
	var v nhl.EdgeTeamComparison
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge team comparison %s: %w", r.TeamID, err)
	}
	return &v, nil
}

func (r EdgeTeamComparison) Format(obj *nhl.EdgeTeamComparison) ([]byte, error) {
	return json.Marshal(obj)
}

// ===== Edge Landing Resources =====

// EdgeSkaterLanding represents league-wide skater Edge leaders.
type EdgeSkaterLanding struct {
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeSkaterLanding) Path() string {
	return fmt.Sprintf("edge/landing/skater-%s.json", edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeSkaterLanding) URL() string {
	return fmt.Sprintf("%s/edge/skater-landing/%s/%d", baseURLAPIWebV1, r.Season.APIString(), r.GameType.Int())
}

func (r EdgeSkaterLanding) Type() core.FileType { return core.EdgeSkaterLanding }

func (r EdgeSkaterLanding) Parse(data []byte) (*nhl.EdgeSkaterLanding, error) {
	var v nhl.EdgeSkaterLanding
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge skater landing: %w", err)
	}
	return &v, nil
}

func (r EdgeSkaterLanding) Format(obj *nhl.EdgeSkaterLanding) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeGoalieLanding represents league-wide goalie Edge leaders.
type EdgeGoalieLanding struct {
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeGoalieLanding) Path() string {
	return fmt.Sprintf("edge/landing/goalie-%s.json", edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeGoalieLanding) URL() string {
	return fmt.Sprintf("%s/edge/goalie-landing/%s/%d", baseURLAPIWebV1, r.Season.APIString(), r.GameType.Int())
}

func (r EdgeGoalieLanding) Type() core.FileType { return core.EdgeGoalieLanding }

func (r EdgeGoalieLanding) Parse(data []byte) (*nhl.EdgeGoalieLanding, error) {
	var v nhl.EdgeGoalieLanding
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge goalie landing: %w", err)
	}
	return &v, nil
}

func (r EdgeGoalieLanding) Format(obj *nhl.EdgeGoalieLanding) ([]byte, error) {
	return json.Marshal(obj)
}

// EdgeTeamLanding represents league-wide team Edge leaders.
type EdgeTeamLanding struct {
	Season   nhl.Season
	GameType nhl.GameType
}

func (r EdgeTeamLanding) Path() string {
	return fmt.Sprintf("edge/landing/team-%s.json", edgeSeasonGameType(r.Season, r.GameType))
}

func (r EdgeTeamLanding) URL() string {
	return fmt.Sprintf("%s/edge/team-landing/%s/%d", baseURLAPIWebV1, r.Season.APIString(), r.GameType.Int())
}

func (r EdgeTeamLanding) Type() core.FileType { return core.EdgeTeamLanding }

func (r EdgeTeamLanding) Parse(data []byte) (*nhl.EdgeTeamLanding, error) {
	var v nhl.EdgeTeamLanding
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse edge team landing: %w", err)
	}
	return &v, nil
}

func (r EdgeTeamLanding) Format(obj *nhl.EdgeTeamLanding) ([]byte, error) {
	return json.Marshal(obj)
}
