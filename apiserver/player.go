package apiserver

import (
	"net/http"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/gin-gonic/gin"
)

func HandleGetPlayers(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		var players []model.Player
		if result := yfh.GormDB.Preload("NHLTeam").Preload("NHLTeam.NHLDivision").Preload("NHLTeam.NHLDivision.NHLConference").Find(&players); result.Error == nil {
			ctx.JSON(http.StatusOK, players)
		} else {
			HandleError(ctx, http.StatusInternalServerError, result.Error)
		}
	}
}
