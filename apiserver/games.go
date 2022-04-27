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
	Game        model.Game
	GithubFiles []*core.GithubFile
}

func HandleImportGames(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportGames,
			Data: map[string]string{
				core.TaskDataUser: DefaultUser,
				core.TaskDataDate: ctx.Param("date"),
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
				core.TaskDataDate:     ctx.Param("date"),
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

func getGames(ctx context.Context, yfh *core.YFH, date time.Time) ([]*Game, error) {
	var games []model.Game
	result := yfh.GormDB.Preload("HomeTeam").Preload("AwayTeam").Find(&games)
	if result.Error != nil {
		return nil, result.Error
	}
	games2 := []*Game{} //  make([]*Game, len(games))
	for _, g := range games {
		files, err := yfh.Github.FindGame(ctx, date, g.Name)
		if err != nil {
			return nil, err
		}
		games2 = append(games2, &Game{Game: g, GithubFiles: files})
	}
	return games2, nil
}
