package cmd

import (
	"testing"

	"github.com/sperano/yfh/config"
	"github.com/spf13/viper"
)

func BenchmarkGetAllStats(b *testing.B) {
	viper.Set(config.FlagDataPath, "../test-data/cache")
	viper.Set(config.FlagSeasons, "../test-data/config/seasons.yaml")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := getAllStats()
		if err != nil {
			b.Fatal(err)
		}
	}
}
