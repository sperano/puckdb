package apiserver

import (
	"errors"
	"net/http"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type League struct {
	League      model.League
	GithubFiles []*core.GithubFile
}

func HandleImportLeague(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportLeague,
			Data: map[string]string{
				core.TaskDataUser: DefaultUser,
			},
		})
	}
}

func HandleGetLeague(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		if files, err := yfh.Github.FindLeague(ctx); err == nil {
			var league model.League
			result := yfh.GormDB.First(&league)
			if result.Error == nil {
				ctx.JSON(http.StatusOK, &League{
					League:      league,
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
