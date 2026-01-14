package database

import (
	"gorm.io/gorm/clause"
	"time"

	"gorm.io/gorm"
)

type RosterPlayer struct {
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         gorm.DeletedAt `gorm:"index"`
	Date              time.Time      `gorm:"primaryKey"`
	TeamID            uint           `gorm:"primaryKey"`
	Team              Team
	PlayerID          uint `gorm:"primaryKey"`
	NHLTeamID         uint
	NHLTeam           NHLTeam
	EligiblePositions PositionName `gorm:"type:int"`
	SelectedPosition  PositionName `gorm:"type:int"`
	SourceVersion     time.Time
}

func (rp *RosterPlayer) SetSourceVersion(time time.Time) {
	rp.SourceVersion = time
}

func (r RosterPlayers) SetSourceVersion(time time.Time) {
	for _, p := range r {
		p.SourceVersion = time
	}
}

type RosterPlayers []RosterPlayer

var rosterUpdateCols = []string{
	CNHLTeamID, CEligiblePositions, CSelectedPosition, CSourceVersion,
}

func (r RosterPlayers) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: CDate}, {Name: CTeamID}, {Name: CPlayerID}},
		DoUpdates: clause.AssignmentColumns(rosterUpdateCols),
	}).Create(r).Error
}

func DiffPlayerRoster(current *RosterPlayer, previous *RosterPlayer) []DifferentAttr {
	// TODO
	return nil
}
