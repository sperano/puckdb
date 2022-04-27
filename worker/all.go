package worker

import (
	"context"
	"time"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func HandleImportAll(ctx context.Context, yfh *core.YFH) error {
	if err := core.Publish(yfh.Queue, &core.Task{
		Type: core.TaskImportFantasyGame,
		Data: map[string]string{
			core.TaskDataUser: ctx.Value(core.CtxUser).(string),
		},
	}); err != nil {
		return err
	}
	if err := core.Publish(yfh.Queue, &core.Task{
		Type: core.TaskImportLeague,
		Data: map[string]string{
			core.TaskDataUser: ctx.Value(core.CtxUser).(string),
		},
	}); err != nil {
		return err
	}
	if err := HandleImportTeams(ctx, yfh); err != nil {
		return err
	}
	dates, err := core.GetDateRange("", "")
	if err != nil {
		return err
	}
	for _, date := range dates {
		if err := core.Publish(yfh.Queue, &core.Task{
			Type: core.TaskImportGames,
			Data: map[string]string{
				core.TaskDataUser: ctx.Value(core.CtxUser).(string),
				core.TaskDataDate: core.GetShortTimestamp(date),
			},
		}); err != nil {
			return err
		}
		if err := core.Publish(yfh.Queue, &core.Task{
			Type: core.TaskImportRosters,
			Data: map[string]string{
				core.TaskDataUser: ctx.Value(core.CtxUser).(string),
				core.TaskDataDate: core.GetShortTimestamp(date),
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func InitMeta(ctx context.Context, db *gorm.DB, mtype model.MetaType, day *time.Time) error {
	var d time.Time
	if day != nil {
		d = *day
	}
	meta := model.Meta{
		Type:             mtype,
		Date:             d,
		Imported:         false,
		GithubFilesCount: 0,
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"type", "date", "imported", "github_files_count",
		}),
	}).Create(&meta).Error
}

func HandleInitMeta(ctx context.Context, db *gorm.DB) error {
	log.Info("Initializing meta...")
	if err := InitMeta(ctx, db, model.MetaFantasyGame, nil); err != nil {
		return err
	}
	if err := InitMeta(ctx, db, model.MetaLeague, nil); err != nil {
		return err
	}
	day := core.GetSeasonStart()
	end := core.GetSeasonEnd()
	for {
		if err := InitMeta(ctx, db, model.MetaGame, &day); err != nil {
			return err
		}
		day = day.AddDate(0, 0, 1)
		if day == end {
			break
		}
	}
	return nil
}
