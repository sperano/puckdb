package worker

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/spf13/viper"
)

// doDownload ensures a file is downloaded and cached.
// Uses the production file system.
func doDownload(ctx context.Context, file cache.File, url string) error {
	fs := cache.NewSimpleCache()
	return doDownloadImpl(ctx, fs, file, url)
}

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
			if err := saveToCache(fs, file, content); err != nil {
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

// doImport downloads (if needed) and imports a file into the database.
// Uses the production file system.
func doImport[T database.Object](ctx context.Context, file cache.File, url string, mg cache.Getter[T]) (T, error) {
	fs := cache.NewSimpleCache()
	return doImportImpl(ctx, fs, file, url, mg)
}

// doImportImpl is the testable implementation.
func doImportImpl[T database.Object](ctx context.Context, fs cache.FileSystem, file cache.File, url string, mg cache.Getter[T]) (T, error) {
	if err := doDownloadImpl(ctx, fs, file, url); err != nil {
		return nil, err
	}
	log.Info().Str("file", cache.Path(file)).Msg("Importing")
	data, err := fs.Read(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cache.Path(file), err)
	}
	fantasy, err := cache.ParseXML(data)
	if err != nil {
		log.Warn().Int("data size", len(data)).Str("data", string(data)).Msgf("deleting %s because of parse XML error: %s, will re-download again", cache.Path(file), err)
		return nil, fs.Remove(file)
	}
	obj, err := mg(fantasy)
	if err != nil {
		return nil, fmt.Errorf("%s: error converting %T: %w", cache.Path(file), obj, err)
	}
	// Get file modification time as source version
	info, err := os.Stat(fs.FullPath(file))
	if err == nil {
		obj.SetSourceVersion(info.ModTime())
	}
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	err = obj.Ensure(db)
	if err != nil {
		return nil, fmt.Errorf("%s: error importing in db: %w", cache.Path(file), err)
	}
	log.Debug().Msgf("Imported %T into database", obj)
	return obj, nil
}

func saveToCache(fs cache.FileSystem, file cache.File, content []byte) error {
	if err := fs.Write(file, content); err != nil {
		return err
	}
	log.Info().Msgf("Saved %s", cache.Path(file))
	return nil
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
