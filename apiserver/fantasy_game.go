package apiserver

import (
	"errors"
	"net/http"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type FantasyGame struct {
	FantasyGame model.FantasyGame
	GithubFiles []*core.GithubFile
}

func HandleImportFantasyGame(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportFantasyGame,
			Data: map[string]string{
				core.TaskDataUser: DefaultUser,
			},
		})
	}
}

// TODO maybe not return a 404 for a not found
// because we still want to have the github files, if any
func HandleGetFantasyGame(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		if files, err := yfh.Github.FindFantasyGame(ctx); err == nil {
			var fantasyGame model.FantasyGame
			result := yfh.GormDB.First(&fantasyGame)
			if result.Error == nil {
				ctx.JSON(http.StatusOK, &FantasyGame{
					FantasyGame: fantasyGame,
					GithubFiles: files,
				})
			} else {
				if errors.Is(result.Error, gorm.ErrRecordNotFound) {
					HandleError(ctx, http.StatusNotFound, result.Error)
				} else {
					HandleError(ctx, http.StatusInternalServerError, result.Error)
				}
			}
		} else {
			HandleError(ctx, http.StatusInternalServerError, err)
		}
	}
}
