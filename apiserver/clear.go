package apiserver

import (
	"context"
	"net/http"

	"github.com/ericsperano/yfh/core"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func ClearDB(ctx context.Context, yfh *core.YFH) error {
	if err := core.DropEverything(yfh.GormDB); err != nil {
		return err
	}
	if err := core.DoMigration(yfh.GormDB); err != nil {
		return err
	}
	if err := core.EnsureNHL(yfh.GormDB); err != nil {
		return err
	}
	core.Publish(yfh.Queue, &core.Task{
		Type: core.TaskInitMeta,
	})
	return nil
}

func HandleClearDB(yfh *core.YFH) func(*gin.Context) {
	return func(ctx *gin.Context) {
		if err := ClearDB(ctx, yfh); err != nil {
			HandleError(ctx, http.StatusInternalServerError, err)
		}
	}
}

func ClearCache(ctx context.Context, yfh *core.YFH) error {
	keys := yfh.RedisClient.Keys(ctx, "yfh*")
	if err := keys.Err(); err != nil {
		return err
	}
	result, err := keys.Result()
	if err != nil {
		return err
	}
	for _, key := range result {
		log.Debugf("Clearing key %s", key)
		if err := yfh.RedisClient.Del(ctx, key).Err(); err != nil {
			return err
		}
	}
	return nil
}

func HandleClearCache(yfh *core.YFH) func(*gin.Context) {
	return func(ctx *gin.Context) {
		if err := ClearCache(ctx, yfh); err != nil {
			HandleError(ctx, http.StatusInternalServerError, err)
		}
	}
}
