package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type GithubCache interface {
	GetDirectoryContents(context.Context, string) ([]*GithubFile, error)
	SetDirectoryContents(context.Context, string, []*GithubFile) error
	InvalidateDirectoryContents(context.Context, string) error
	GetFile(ctx context.Context, path string) ([]byte, error)
	SetFile(ctx context.Context, path string, data []byte) error
}

type RedisGithubCache struct {
	RedisClient *redis.Client
	Duration    time.Duration
}

func NewRedisGithubCache(redisClient *redis.Client) *RedisGithubCache {
	return &RedisGithubCache{
		RedisClient: redisClient,
		Duration:    time.Duration(viper.GetInt(FlagRedisCacheDuration)) * time.Minute,
	}
}

func (rgc *RedisGithubCache) GetDirectoryContents(ctx context.Context, dir string) ([]*GithubFile, error) {
	jsonData, err := GetRedisValue(ctx, rgc.RedisClient, fmt.Sprintf("yfh-%s", dir))
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	log.Tracef("GithubCache Get: dir=%s jsonData=%s", dir, jsonData)
	var files []*GithubFile
	err = json.Unmarshal([]byte(jsonData), &files)
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (rgc *RedisGithubCache) SetDirectoryContents(ctx context.Context, dir string, contents []*GithubFile) error {
	data, err := json.Marshal(contents)
	if err != nil {
		return err
	}
	jsonData := string(data)
	log.Tracef("GithubCache Set: dir=%s jsonData=%s", dir, jsonData)
	return rgc.RedisClient.Set(ctx, fmt.Sprintf("yfh-%s", dir), jsonData, rgc.Duration).Err()
}

func (rgc *RedisGithubCache) InvalidateDirectoryContents(ctx context.Context, dir string) error {
	log.Debugf("GithubCache Invalidating: %s", dir)
	return rgc.RedisClient.Del(ctx, fmt.Sprintf("yfh-%s", dir)).Err()
}

func (rgc *RedisGithubCache) GetFile(ctx context.Context, path string) ([]byte, error) {
	log.Debugf("GithubCache GetFile: %s", path)
	str, err := GetRedisValue(ctx, rgc.RedisClient, fmt.Sprintf("yfh-%s", path))
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	log.Tracef("GithubCache Get: path=%s str=%s", path, str)
	return []byte(str), nil
}

func (rgc *RedisGithubCache) SetFile(ctx context.Context, path string, data []byte) error {
	log.Debugf("GithubCache GetFile: %s", path)
	return rgc.RedisClient.Set(ctx, fmt.Sprintf("yfh-%s", path), string(data), rgc.Duration).Err()
}
