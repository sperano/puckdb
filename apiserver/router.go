package apiserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ericsperano/yfh/core"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/penglongli/gin-metrics/ginmetrics"
	log "github.com/sirupsen/logrus"
)

const DefaultUser = "eric"

func HandleError(ctx *gin.Context, code int, err error) {
	log.Error(err.Error())
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

func SetupRouter(yfh *core.YFH) *gin.Engine {
	r := gin.Default()
	//r.SetTrustedProxies(nil)
	r.Use(cors.Default())

	// configure metrics middleware
	monitor := ginmetrics.GetMonitor()
	core.CreateMetricTaskPublished(monitor)
	monitor.SetMetricPath("/metrics")
	monitor.Use(r)

	r.GET("/ping", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, "pong")
	})

	r.GET("/fantasy_game", HandleGetFantasyGame(yfh))
	r.GET("/league", HandleGetLeague(yfh))
	r.GET("/players", HandleGetPlayers(yfh))
	r.GET("/teams", HandleGetTeams(yfh))
	r.GET("/nhl/teams", HandleGetNHLTeams(yfh))
	r.GET("/nhl/divisions", HandleGetNHLDivisions(yfh))
	r.GET("/nhl/conferences", HandleGetNHLConferences(yfh))
	r.GET("/:date/games", HandleGetGames(yfh))

	r.DELETE("/cache", HandleClearCache(yfh))
	r.DELETE("/db", HandleClearDB(yfh))
	r.DELETE("/cache/fantasy_game", HandleInvalidateCache(yfh, "yfh-fantasy-game*"))
	r.DELETE("/cache/league", HandleInvalidateCache(yfh, "yfh-league*"))
	r.DELETE("/cache/teams", HandleInvalidateCache(yfh, "yfh-team-*"))

	r.POST("/import/fantasy_game", HandleImportFantasyGame(yfh, Publish))
	r.POST("/import/league", HandleImportLeague(yfh, Publish))
	r.POST("/import/teams", HandleImportTeams(yfh, Publish))
	r.POST("/import/team/:team_id", HandleImportTeam(yfh, Publish))
	r.POST("/import/:date/games", HandleImportGames(yfh, Publish))
	r.POST("/import/:date/games/:game", HandleImportGame(yfh, Publish))
	r.POST("/import/:date/rosters/:team_id", HandleImportRoster(yfh, Publish))
	r.POST("/import/:date/rosters", HandleImportRosters(yfh, Publish))
	r.POST("/import/all", HandleImportAll(yfh, Publish))

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
	return r
}
