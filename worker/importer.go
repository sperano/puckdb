package worker

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/metrics"
	"github.com/spf13/viper"
)

// doDownloadImpl is the testable implementation.
func doDownloadImpl(ctx context.Context, fs cache.FileSystem, file cache.File, url string) error {
	fileType := reflect.TypeOf(file).Name()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
				metrics.IncDownload(fileType, "error")
				return fmt.Errorf("%s: %w", file.Dir(), err)
			}
			if fs.Exists(file) {
				log.Info().Str("file", cache.Path(file)).Msg("Already downloaded")
				metrics.IncDownload(fileType, "hit")
				return nil
			}
			content, err := DownloadFromYahoo(url)
			if err != nil {
				metrics.IncDownload(fileType, "error")
				return fmt.Errorf("%s: %w", url, err)
			}
			if err := fs.Write(file, content); err != nil {
				metrics.IncDownload(fileType, "error")
				return fmt.Errorf("%s: %w", cache.Path(file), err)
			}
			log.Info().Str("file", cache.Path(file)).Msg("Downloaded")
			metrics.IncDownload(fileType, "miss")
			sleepAfterYahooDownload()
			return nil
		}
	}
}

// sleepAfterYahooDownload sleeps for a random duration between min and max after a Yahoo API download.
func sleepAfterYahooDownload() {
	minSeconds := viper.GetInt(config.FlagYahooDownloadSleepMin)
	maxSeconds := viper.GetInt(config.FlagYahooDownloadSleepMax)
	if maxSeconds <= 0 {
		return
	}
	if minSeconds < 0 {
		minSeconds = 0
	}
	if minSeconds >= maxSeconds {
		minSeconds = maxSeconds
	}
	sleepSeconds := minSeconds + rand.Intn(maxSeconds-minSeconds+1)
	if sleepSeconds > 0 {
		log.Debug().Int("seconds", sleepSeconds).Msg("Sleeping after Yahoo download")
		time.Sleep(time.Duration(sleepSeconds) * time.Second)
	}
}
