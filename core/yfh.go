package core

import (
	"context"

	"github.com/adjust/rmq/v4"
	"github.com/go-redis/redis/v8"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

/**
 * Key for the user in the context
 * https://stackoverflow.com/questions/40891345/fix-should-not-use-basic-type-string-as-key-in-context-withvalue-golint
 */
type CtxKey int

const (
	CtxUser CtxKey = iota
	//CtxYFH
)

func GetCtxUser(ctx context.Context) string {
	return ctx.Value(CtxUser).(string)
}

type YFH struct {
	ErrChan       chan error
	RedisClient   *redis.Client
	RmqConnection rmq.Connection
	Queue         rmq.Queue
	Local         *LocalClient
	GormDB        *gorm.DB
}

func NewYFH() (*YFH, error) {
	yfh := &YFH{}
	yfh.ErrChan = make(chan error, 10)
	yfh.RedisClient = NewRedisClient()

	rmqConnection, queue, err := NewRMQConnectionAndQueue("consumer", yfh.RedisClient, yfh.ErrChan)
	if err != nil {
		return nil, err
	}
	yfh.RmqConnection = rmqConnection
	yfh.Queue = queue

	yfh.Local = NewLocalClient(viper.GetString(FlagDataPath))
	db, err := OpenGorm()
	if err != nil {
		return nil, err
	}
	yfh.GormDB = db
	return yfh, nil
}

func (yfh *YFH) Close() {
	yfh.RedisClient.Close()
}

func NewRMQConnectionAndQueue(name string, redisClient *redis.Client, errChan chan error) (rmq.Connection, rmq.Queue, error) {
	connection, err := rmq.OpenConnectionWithRedisClient(name, redisClient, errChan)
	if err != nil {
		return nil, nil, err
	}
	queue, err := connection.OpenQueue(QueueTasks)
	if err != nil {
		return nil, nil, err
	}
	return connection, queue, nil
}
