package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adjust/rmq/v4"
	"github.com/go-redis/redis/v8"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v8"
	"github.com/penglongli/gin-metrics/ginmetrics"
	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"

	"github.com/spf13/viper"
)

const QueueTasks = "yfh-tasks"

const FlagRedisURL = "redis_url"
const FlagRedisPassword = "redis_password"
const FlagRedisDB = "redis_db"

func SetupViperRedis(flags *flag.FlagSet) {
	flags.String(FlagRedisURL, "localhost:6379", "Redis url")
	viper.BindPFlag(FlagRedisURL, flags.Lookup(FlagRedisURL))

	flags.String(FlagRedisPassword, "", "Redis password")
	viper.BindPFlag(FlagRedisPassword, flags.Lookup(FlagRedisPassword))

	flags.Int(FlagRedisDB, 0, "Redis db") // TODO 1 should be managed by pulumi_home
	viper.BindPFlag(FlagRedisDB, flags.Lookup(FlagRedisDB))
}

func NewRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     viper.GetString(FlagRedisURL),
		Password: viper.GetString(FlagRedisPassword),
		DB:       viper.GetInt(FlagRedisDB),
	})
}

func NewRedSync(client *redis.Client) *redsync.Redsync {
	fmt.Printf("%v\n", client)
	pool := goredis.NewPool(client) // or, pool := redigo.NewPool(...)
	// Create an instance of redisync to be used to obtain a mutual exclusion ock.
	return redsync.New(pool)
}

/*
const T9nApps = "t9n.apps"
const T9nPulumiProjects = "t9n.pulumi_projects"
const RefreshStartedAt = "t9n.refresh.started_at"

const FmtImageName = "t9n.app.%s.image.name"
const FmtImageTag = "t9n.app.%s.image.tag"
const FmtRepository = "t9n.app.%s.repository"
const FmtGithubSHA = "t9n.app.%s.github-sha"
const FmtGithubCreatedAt = "t9n.app.%s.git_created_at"
const FmtFindImageTaskStartedAt = "t9n.app.%s.task_started_at"
const FmtFindImageTaskEndedAt = "t9n.app.%s.task_ended_at"

const FmtPulumiTaskStartedAt = "t9n.pulumi_project.%s.task_started_at"
const FmtPulumiTaskEndedAt = "t9n.pulumi_project.%s.task_ended_at"
const FmtPulumiOK = "t9n.pulumi_project.%s.ok"
const FmtPulumiOutput = "t9n.pulumi_project.%s.output"
*/
func Publish(queue rmq.Queue, task *Task) error {
	taskBytes, err := json.Marshal(task)
	if err != nil {
		return err
	}
	log.Debugf("Publishing: %s", string(taskBytes))
	if err = queue.PublishBytes(taskBytes); err != nil {
		return fmt.Errorf("error while publishing %+v to the queue: %w", task, err)
	}
	ginmetrics.GetMonitor().GetMetric(MetricTaskPublished).Inc([]string{task.Type})
	return nil
}

func GetRedisValue(c context.Context, rdb *redis.Client, key string) (string, error) {
	status := rdb.Get(c, key)
	if err := status.Err(); err != nil {
		return "", err
	}
	result, err := status.Result()
	if err != nil {
		return "", err
	}
	return result, nil
}
