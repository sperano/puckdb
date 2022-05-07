package apiserver

import (
	"net/http"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/gin-gonic/gin"
)

func HandleGetNHLTeams(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		var teams []*model.NHLTeam
		result := yfh.GormDB.Preload("NHLDivision").Preload("NHLConference").Find(&teams)
		if result.Error == nil {
			ctx.JSON(http.StatusOK, teams)
		} else {
			HandleError(ctx, http.StatusInternalServerError, result.Error)
		}
	}
}

func HandleGetNHLConferences(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		var confs []*model.NHLConference
		result := yfh.GormDB.Find(&confs)
		if result.Error == nil {
			ctx.JSON(http.StatusOK, confs)
		} else {
			HandleError(ctx, http.StatusInternalServerError, result.Error)
		}
	}
}

func HandleGetNHLDivisions(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		var divisions []*model.NHLDivision
		result := yfh.GormDB.Preload("NHLConference").Find(&divisions)
		if result.Error == nil {
			ctx.JSON(http.StatusOK, divisions)
		} else {
			HandleError(ctx, http.StatusInternalServerError, result.Error)
		}
	}
}
