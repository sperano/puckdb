package model

import (
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var PlayerUpdateCols = []string{
	"key",
	"first_name",
	"last_name",
	"uniform_number",
	"home_url",
	"image_small",
	"image_medium",
	"image_large",
	"nhl_team_id",
}

type Player struct {
	gorm.Model
	ID            int
	Key           string
	FirstName     string
	LastName      string
	UniformNumber int
	HomeURL       string
	ImageSmall    string
	ImageMedium   string
	ImageLarge    string
	NHLTeamID     uint
	NHLTeam       NHLTeam
	//PrimaryPositionID PositionID `json:"primary_position_id"`
	//EditorialPlayerKey string
}

func (p *Player) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns(PlayerUpdateCols),
	}).Create(p).Error
}

var PlayerStatsUpdateCols = []string{
	//"date",
	//"player_id",
	"nhl_team_id",
	"goal_against", "shots_against", "saves", "save_percentage", "goalie_time_on_ice",
	"goals", "assists", "plus_minus", "penalty_minutes", "shots_on_goal", "faceoffs_won", "faceoffs_lost",
	"hits", "blocks", "time_on_ice", "face_off_percentage", "shifts", "take_aways", "give_aways",
}

type PlayerStats struct {
	Date              time.Time `gorm:"primaryKey"`
	PlayerID          uint      `gorm:"primaryKey"`
	Player            Player
	NHLTeamID         uint `gorm:"index"`
	NHLTeam           NHLTeam
	GoalAgainst       uint
	ShotsAgainst      uint
	Saves             uint
	SavePercentage    uint
	GoalieTimeOnIce   uint
	Goals             uint
	Assists           uint
	PlusMinus         int
	PenaltyMinutes    uint
	ShotsOnGoal       uint
	FaceoffsWon       uint
	FaceoffsLost      uint
	Hits              uint
	Blocks            uint
	TimeOnIce         uint
	FaceOffPercentage uint
	Shifts            uint
	TakeAways         uint
	GiveAways         uint
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         gorm.DeletedAt `gorm:"index"`
}

func (ps *PlayerStats) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "date"}, {Name: "player_id"}}, // TODO constants
		DoUpdates: clause.AssignmentColumns(PlayerStatsUpdateCols),
	}).Create(ps).Error
}

func MaybeSetStatUInt(ptr *uint, str string) error {
	if len(str) > 0 {
		if i, err := strconv.Atoi(str); err != nil {
			return err
		} else {
			*ptr = uint(i)
		}
	}
	return nil
}

func MaybeSetStatInt(ptr *int, str string) error {
	if len(str) > 0 {
		if i, err := strconv.Atoi(str); err != nil {
			return err
		} else {
			*ptr = i
		}
	}
	return nil
}

func MaybeSetStatPercentage(ptr *uint, str string) error {
	return MaybeSetStatUInt(ptr, strings.ReplaceAll(str, ".", ""))
}
