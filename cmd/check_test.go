package cmd

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/redis"
	"github.com/spf13/viper"
)

func BenchmarkGetAllMetrics(b *testing.B) {
	viper.Set(config.FlagDataPath, "../test-data/cache")
	viper.Set(config.FlagSeasonYear, 2022)

	redisClient := redis.NewClient()
	defer redisClient.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := getAllMetrics(context.Background(), redisClient)
		if err != nil {
			b.Fatal(err)
		}
	}
}
