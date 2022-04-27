package apiserver

import (
	"github.com/ericsperano/yfh/core"
	"github.com/gin-gonic/gin"
)

func HandleImportAll(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportAll,
			Data: map[string]string{
				core.TaskDataUser: DefaultUser,
			},
		})
	}
}
