package worker

import (
	"context"

	"github.com/ericsperano/yfh/core/model"
	"github.com/ericsperano/yfh/core/xmlmodel"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func EnsureNHLTeam(ctx context.Context, db *gorm.DB, teamInfo *xmlmodel.TeamInfo) error {
	team, err := teamInfo.ToNHLTeamModel()
	if err != nil {
		return err
	}
	conf := model.NHLConference{
		Model: gorm.Model{
			ID: team.NHLConferenceID,
		},
		Name: teamInfo.Conference,
	}
	log.Infof("Ensuring conference %s in db", teamInfo.Conference)
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name"}),
	}).Create(&conf).Error; err != nil {
		return err
	}
	div := model.NHLDivision{
		Model: gorm.Model{
			ID: team.NHLDivisionID,
		},
		Name:            teamInfo.Division,
		NHLConferenceID: conf.ID,
	}
	log.Infof("Ensuring division %s in db", teamInfo.Division)
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name"}),
	}).Create(&div).Error; err != nil {
		return err
	}
	log.Infof("Ensuring team %s in db", team.Name)
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name"}),
	}).Create(&team).Error
}
