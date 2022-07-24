package apiserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/gin-gonic/gin"
)

type Game struct {
	Game       model.Game
	LocalFiles []*core.LocalFile
}

func HandleImportGames(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportGames,
			Data: map[string]string{
				core.TaskDataUser: DefaultUser,
				core.TaskDataDate: ctx.Param("date"), // TODO constants
			},
		})
	}
}

func HandleImportGame(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportGame,
			Data: map[string]string{
				core.TaskDataUser:     DefaultUser,
				core.TaskDataDate:     ctx.Param("date"), // TODO constants
				core.TaskDataGameLink: fmt.Sprintf("%s/nhl/%s/", core.BaseSportsURL, ctx.Param("game")),
			},
		})
	}
}

func HandleGetGames(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		date, err := core.ParseShortTimestamp(ctx.Param("date"))
		if err == nil {
			games, err := getGames(ctx, yfh, date)
			if err == nil {
				ctx.JSON(http.StatusOK, games)
			} else {
				HandleError(ctx, http.StatusInternalServerError, err)
			}
		} else {
			HandleError(ctx, http.StatusInternalServerError, err)
		}
	}
}

func HandleInvalidateCacheGame(yfh *core.YFH) func(*gin.Context) {
	return func(ctx *gin.Context) {
		date, err := core.ParseShortTimestamp(ctx.Param("date"))
		if err != nil {
			HandleError(ctx, http.StatusInternalServerError, err)
			return
		}
		filter := fmt.Sprintf("yfh-games/%04d/%02d/%02d/%s*", date.Year(), date.Month(), date.Day(), ctx.Param("game"))
		if err := ClearCacheWithFilter(ctx, yfh, filter); err != nil {
			HandleError(ctx, http.StatusInternalServerError, err)
		}
	}
}

func getGames(ctx context.Context, yfh *core.YFH, date time.Time) ([]*Game, error) {
	var games []model.Game
	result := yfh.GormDB.Preload("HomeTeam").Preload("AwayTeam").Find(&games)
	if result.Error != nil {
		return nil, result.Error
	}
	games2 := []*Game{} //  make([]*Game, len(games))
	for _, g := range games {
		files, err := yfh.Local.FindGame(ctx, date, g.Name)
		if err != nil {
			return nil, err
		}
		games2 = append(games2, &Game{Game: g, LocalFiles: files})
	}
	return games2, nil
}
