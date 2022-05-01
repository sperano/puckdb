package apiserver

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/gin-gonic/gin"
)

type Team struct {
	Team        model.Team
	GithubFiles []*core.GithubFile
}

func HandleImportTeams(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportTeams,
			Data: map[string]string{
				core.TaskDataUser: DefaultUser,
			},
		})
	}
}

func HandleImportTeam(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		publish(ctx, yfh, &core.Task{
			Type: core.TaskImportTeams,
			Data: map[string]string{
				core.TaskDataUser:   DefaultUser,
				core.TaskDataTeamID: ctx.Param(core.TaskDataTeamID),
			},
		})
	}
}

func HandleInvalidateCacheTeam(yfh *core.YFH) func(*gin.Context) {
	return func(ctx *gin.Context) {
		teamID, err := strconv.Atoi(ctx.Param(core.TaskDataTeamID))
		if err != nil {
			HandleError(ctx, http.StatusInternalServerError, err)
			return
		}
		filter := fmt.Sprintf("yfh-team-%02d*", teamID)
		if err := ClearCacheWithFilter(ctx, yfh, filter); err != nil {
			HandleError(ctx, http.StatusInternalServerError, err)
		}
	}
}

func HandleGetTeams(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		var teams []*Team
		var err error
		var team *Team
		for _, id := range core.GetTeamIDs() {
			team, err = getTeam(ctx, yfh, id)
			if err != nil {
				break
			}
			teams = append(teams, team)
		}
		if err == nil {
			ctx.JSON(http.StatusOK, teams)
		} else {
			HandleError(ctx, http.StatusInternalServerError, err)
		}
	}
}

func getTeam(ctx context.Context, yfh *core.YFH, teamID uint) (*Team, error) {
	files, err := yfh.Github.FindTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	var team model.Team
	if result := yfh.GormDB.First(&team, teamID); result.Error != nil {
		return nil, result.Error
	}
	return &Team{Team: team, GithubFiles: files}, nil
}

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
