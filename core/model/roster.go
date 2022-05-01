package model

import (
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RosterPosition int

const (
	BN   RosterPosition = 1
	IR   RosterPosition = 2 ^ 1
	C    RosterPosition = 2 ^ 2
	LW   RosterPosition = 2 ^ 3
	RW   RosterPosition = 2 ^ 4
	D    RosterPosition = 2 ^ 5
	Util RosterPosition = 2 ^ 6
	G    RosterPosition = 2 ^ 7
)

var _pos_labels = []string{"BN", "IR", "C", "LW", "RW", "D", "Util", "G"}

func (rp *RosterPosition) String() string {
	return _pos_labels[*rp]
}

func ParseRosterPosition(str string) (RosterPosition, error) {
	switch str {
	case "BN":
		return BN, nil
	case "IR":
		return IR, nil
	case "C":
		return C, nil
	case "LW":
		return LW, nil
	case "RW":
		return RW, nil
	case "D":
		return D, nil
	case "Util":
		return Util, nil
	case "G":
		return G, nil
	}
	return -1, fmt.Errorf("can't parse RosterPosition: %s", str)
}

// TODO better name than date
type RosterPlayer struct {
	Date              time.Time `gorm:"primaryKey"`
	TeamID            uint      `gorm:"primaryKey"`
	Team              Team
	PlayerID          uint `gorm:"primaryKey"`
	Player            Player
	NHLTeamID         uint
	NHLTeam           NHLTeam
	EligiblePositions RosterPosition `gorm:"type:int"`
	SelectedPosition  RosterPosition `gorm:"type:int"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         gorm.DeletedAt `gorm:"index"`
}

func (rp *RosterPlayer) Ensure(db *gorm.DB) error {
	p := Player{
		Model: gorm.Model{
			ID: uint(rp.PlayerID),
		},
		NHLTeamID: rp.NHLTeamID,
	}
	if err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, // TODO constants
		// TODO move that list elsewhere
		DoUpdates: clause.AssignmentColumns([]string{"nhl_team_id"}),
	}).Create(&p).Error; err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "date"}, {Name: "team_id"}, {Name: "player_id"}}, // TODO constants
		// TODO move that list elsewhere
		DoUpdates: clause.AssignmentColumns([]string{"nhl_team_id", "eligible_positions", "selected_position"}),
	}).Create(rp).Error
}
