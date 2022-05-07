package apiserver

import (
	"github.com/ericsperano/yfh/core"
	"github.com/gin-gonic/gin"
)

func HandleImportRoster(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportRoster,
			Data: map[string]string{
				core.TaskDataUser:   DefaultUser,
				core.TaskDataTeamID: ctx.Param(core.TaskDataTeamID),
				core.TaskDataDate:   ctx.Param("date"),
			},
		})
	}
}

func HandleImportRosters(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportRosters,
			Data: map[string]string{
				core.TaskDataUser: DefaultUser,
				core.TaskDataDate: ctx.Param("date"),
			},
		})
	}
}
