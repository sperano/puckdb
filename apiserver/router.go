package apiserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ericsperano/yfh/core"
	docs "github.com/ericsperano/yfh/docs"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/penglongli/gin-metrics/ginmetrics"
	"github.com/rs/zerolog/log"

	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

const DefaultUser = "eric"

func HandleError(ctx *gin.Context, code int, err error) {
	log.Error().Err(err)
	ctx.JSON(code, gin.H{
		"error": err.Error(),
	})
}

type TaskPublisherFn func(*gin.Context, *core.YFH, *core.Task)

func Publish(ctx *gin.Context, yfh *core.YFH, task *core.Task) {
	if err := core.Publish(yfh.Queue, task); err == nil {
		ctx.JSON(http.StatusOK, gin.H{
			"published-task-type": task.Type,
		})
	} else {
		HandleError(ctx, http.StatusInternalServerError, fmt.Errorf("download fantasy game: error publishing: %w", err))
	}
}

func HandleInvalidateCache(yfh *core.YFH, filter string) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		if err := ClearCacheWithFilter(ctx, yfh, filter); err != nil {
			HandleError(ctx, http.StatusInternalServerError, err)
		}
	}
}

func HandleCompute(yfh *core.YFH, publish TaskPublisherFn) func(*gin.Context) {
	return func(ctx *gin.Context) {
		teamID := ctx.Param("team_id")
		date := ctx.Param("date")
		if len(date) == 0 && len(teamID) == 0 {
			publish(ctx, yfh, &core.Task{
				Type: core.TaskComputeAll,
				Data: map[string]string{
					core.TaskDataUser: DefaultUser,
				},
			})
			return
		}
		if len(date) == 0 {
			publish(ctx, yfh, &core.Task{
				Type: core.TaskComputeForTeam,
				Data: map[string]string{
					core.TaskDataUser:   DefaultUser,
					core.TaskDataTeamID: teamID,
				},
			})
			return
		}
		if len(teamID) == 0 {
			publish(ctx, yfh, &core.Task{
				Type: core.TaskComputeForDate,
				Data: map[string]string{
					core.TaskDataUser: DefaultUser,
					core.TaskDataDate: date,
				},
			})
			return
		}
		publish(ctx, yfh, &core.Task{
			Type: core.TaskComputeForDateAndTeam,
			Data: map[string]string{
				core.TaskDataUser:   DefaultUser,
				core.TaskDataDate:   date,
				core.TaskDataTeamID: teamID,
			},
		})

	}
}

func SetupRouter(yfh *core.YFH) *gin.Engine {
	r := gin.Default()
	//r.SetTrustedProxies(nil)
	docs.SwaggerInfo.BasePath = "/api/v1/"
	//docs.SwaggerInfo.BasePath = "/"
	r.Use(cors.Default())

	// configure metrics middleware
	monitor := ginmetrics.GetMonitor()
	core.CreateMetricTaskPublished(monitor)
	monitor.SetMetricPath("/metrics")
	monitor.Use(r)

	r.GET("/ping", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, "pong")
	})

	v1 := r.Group("/api/v1")
	v1.GET("/fantasy_game", HandleGetFantasyGame(yfh))
	v1.GET("/league", HandleGetLeague(yfh))
	v1.GET("/players", HandleGetPlayers(yfh))
	v1.GET("/teams", HandleGetTeams(yfh))
	v1.GET("/nhl/teams", HandleGetNHLTeams(yfh))
	v1.GET("/nhl/divisions", HandleGetNHLDivisions(yfh))
	v1.GET("/nhl/conferences", HandleGetNHLConferences(yfh))
	v1.GET("/:date/games", HandleGetGames(yfh))

	v1.DELETE("/cache", HandleClearCache(yfh))
	v1.DELETE("/cache/fantasy_game", HandleInvalidateCache(yfh, "yfh-fantasy-game*"))
	v1.DELETE("/cache/league", HandleInvalidateCache(yfh, "yfh-league*"))
	v1.DELETE("/cache/teams", HandleInvalidateCache(yfh, "yfh-team-*"))
	v1.DELETE("/cache/teams/:team_id", HandleInvalidateCacheTeam(yfh))
	v1.DELETE("/cache/:date/games", HandleInvalidateCacheGame(yfh))
	v1.DELETE("/cache/:date/games/:game", HandleInvalidateCacheGame(yfh))
	//r.DELETE("/cache/:date/rosters", HandleInvalidateCacheRosters(yfh))
	//r.DELETE("/cache/:date/rosters/:team_id", HandleInvalidateCacheGame(yfh))

	v1.DELETE("/db", HandleClearDB(yfh))

	v1.POST("/import/fantasy_game", HandleImportFantasyGame(yfh, Publish))
	v1.POST("/import/league", HandleImportLeague(yfh, Publish))
	v1.POST("/import/teams", HandleImportTeams(yfh, Publish))
	v1.POST("/import/teams/:team_id", HandleImportTeam(yfh, Publish))
	v1.POST("/import/teams/:team_id/summary/:date", HandleImportTeamSummary(yfh, Publish))
	v1.POST("/import/teams/:team_id/summaries", HandleImportTeamSummaries(yfh, Publish))
	v1.POST("/import/:date/games", HandleImportGames(yfh, Publish))
	v1.POST("/import/:date/games/:game", HandleImportGame(yfh, Publish))
	v1.POST("/import/:date/rosters/:team_id", HandleImportRoster(yfh, Publish))
	v1.POST("/import/:date/rosters", HandleImportRosters(yfh, Publish))
	v1.POST("/import/all", HandleImportAll(yfh, Publish))

	v1.POST("/compute", HandleCompute(yfh, Publish))
	v1.POST("/compute-team/:team_id", HandleCompute(yfh, Publish))
	v1.POST("/compute-date/:date", HandleCompute(yfh, Publish))
	v1.POST("/compute/:date/:team_id", HandleCompute(yfh, Publish))

	yahoo := r.Group("/yahoo")
	yahoo.GET("/login", HandleYahooLogin)
	yahoo.GET("/authenticated", HandleYahooAuthenticated(yfh))

	r.GET("/debug", func(ctx *gin.Context) {
		ctxV := context.WithValue(ctx, core.CtxUser, DefaultUser)
		data := make(map[string]interface{})
		token, err := core.LoadToken(ctxV, yfh)
		if err != nil {
			HandleError(ctx, http.StatusInternalServerError, err)
			return
		}
		data["oauth2_token"] = token
		ctx.JSON(http.StatusOK, data)
	})

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	return r
}
