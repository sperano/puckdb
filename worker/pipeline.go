package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/http"
	"go.temporal.io/sdk/temporal"
	"gorm.io/gorm"
)

const (
	RoutinesPerStage = 2
)

// recoverPanic sends any panic as an error to the provided channel
func recoverPanic(errc chan<- error, name string) {
	if r := recover(); r != nil {
		errc <- fmt.Errorf("panic in %s: %v", name, r)
	}
}

// PipelineItem represents an item flowing through the pipeline
type PipelineItem[ID any, Parsed any] struct {
	Day    time.Time
	ID     ID
	Parsed Parsed
}

// StageFunc is a function that processes items in a pipeline stage
type StageFunc[ID any, Parsed any] func(ctx context.Context, in <-chan *PipelineItem[ID, Parsed]) (<-chan *PipelineItem[ID, Parsed], <-chan error)

// PipelineConfig holds the configuration for a specific pipeline type
type PipelineConfig[ID any, Parsed any] struct {
	FileType   cache.FileType
	IDName     func(ID) string
	Downloader func(ID) ([]byte, error)
	Parser     func(fs cache.FileSystem, file cache.File) (Parsed, error)
	Importer   func(db *gorm.DB, day time.Time, id ID, parsed Parsed) error
	// FSFactory allows injecting a custom FileSystem for testing. Defaults to cache.NewSimpleCache.
	FSFactory func() cache.FileSystem
	// ContinueOnError when true, logs download/parse errors and continues processing other items
	// instead of failing the entire pipeline. Useful for Yahoo game pages that may return 404.
	ContinueOnError bool
}

// getFS returns the FileSystem to use, defaulting to cache.NewSimpleCache if not set
func (c PipelineConfig[ID, Parsed]) getFS() cache.FileSystem {
	if c.FSFactory != nil {
		return c.FSFactory()
	}
	return cache.NewSimpleCache()
}

// Generator creates a channel of pipeline items from a slice of IDs
func Generator[ID any, Parsed any](ctx context.Context, day time.Time, ids []ID) <-chan *PipelineItem[ID, Parsed] {
	out := make(chan *PipelineItem[ID, Parsed])
	go func() {
		defer close(out)
		for _, id := range ids {
			select {
			case <-ctx.Done():
				return
			case out <- &PipelineItem[ID, Parsed]{Day: day, ID: id}:
			}
		}
	}()
	return out
}

// DownloadStage returns a stage function that downloads items
func DownloadStage[ID any, Parsed any](config PipelineConfig[ID, Parsed]) StageFunc[ID, Parsed] {
	return func(ctx context.Context, in <-chan *PipelineItem[ID, Parsed]) (<-chan *PipelineItem[ID, Parsed], <-chan error) {
		out := make(chan *PipelineItem[ID, Parsed])
		errc := make(chan error, 1)
		go func() {
			defer close(errc)
			defer close(out)
			defer recoverPanic(errc, "DownloadStage")

			lfs := config.getFS()
			for item := range in {
				log.Debug().Time("day", item.Day).Str("id", config.IDName(item.ID)).Msg("Check if file is downloaded")
				file := lfs.New(config.FileType, item.Day, item.ID)
				if err := lfs.MkdirAll(file.Dir(), 0755); err != nil {
					errc <- err
					return
				}
				if !lfs.Exists(file) {
					content, err := config.Downloader(item.ID)
					if err != nil {
						// If ContinueOnError is set, log and skip this item
						if config.ContinueOnError {
							log.Warn().Err(err).Str("id", config.IDName(item.ID)).Msg("Download failed, skipping")
							continue
						}
						// Wrap non-retryable client errors (4xx except 404) as non-retryable
						// 404 is retryable because Yahoo Sports sometimes returns intermittent 404s
						var httpErr *http.HTTPError
						if errors.As(err, &httpErr) && httpErr.IsNonRetryableClientError() {
							errc <- temporal.NewNonRetryableApplicationError(
								err.Error(), "HTTPClientError", err)
							return
						}
						// 404 and 5xx errors will be retried by Temporal's retry policy
						errc <- fmt.Errorf("%s: %w", cache.Path(file), err)
						return
					}
					if err = saveToCache(lfs, file, content); err != nil {
						errc <- err
						return
					}
				}
				select {
				case <-ctx.Done():
					return
				case out <- item:
				}
			}
		}()
		return out, errc
	}
}

// ParseStage returns a stage function that parses downloaded items
func ParseStage[ID any, Parsed any](config PipelineConfig[ID, Parsed]) StageFunc[ID, Parsed] {
	return func(ctx context.Context, in <-chan *PipelineItem[ID, Parsed]) (<-chan *PipelineItem[ID, Parsed], <-chan error) {
		out := make(chan *PipelineItem[ID, Parsed])
		errc := make(chan error, 1)
		go func() {
			defer close(errc)
			defer close(out)
			defer recoverPanic(errc, "ParseStage")

			lfs := config.getFS()
			for item := range in {
				log.Debug().Time("day", item.Day).Str("id", config.IDName(item.ID)).Msg("Parsing file")
				file := lfs.New(config.FileType, item.Day, item.ID)
				parsed, err := config.Parser(lfs, file)
				if err != nil {
					// If ContinueOnError is set, log and skip this item
					if config.ContinueOnError {
						log.Warn().Err(err).Str("id", config.IDName(item.ID)).Msg("Parse failed, skipping")
						continue
					}
					errc <- err
					return
				}
				item.Parsed = parsed
				select {
				case <-ctx.Done():
					return
				case out <- item:
				}
			}
		}()
		return out, errc
	}
}

// ImportStage returns a stage function that imports parsed items to DB
func ImportStage[ID any, Parsed any](config PipelineConfig[ID, Parsed]) StageFunc[ID, Parsed] {
	return func(ctx context.Context, in <-chan *PipelineItem[ID, Parsed]) (<-chan *PipelineItem[ID, Parsed], <-chan error) {
		out := make(chan *PipelineItem[ID, Parsed])
		errc := make(chan error, 1)
		go func() {
			defer close(errc)
			defer close(out)
			defer recoverPanic(errc, "ImportStage")

			db, err := getDB()
			if err != nil {
				errc <- err
				return
			}
			for item := range in {
				log.Info().Time("day", item.Day).Str("id", config.IDName(item.ID)).Msg("Importing to database")
				if err := config.Importer(db, item.Day, item.ID, item.Parsed); err != nil {
					errc <- err
					return
				}
				select {
				case <-ctx.Done():
					return
				case out <- item:
				}
			}
		}()
		return out, errc
	}
}

// RunDownloadPipeline runs a download-only pipeline for a day
func RunDownloadPipeline[ID any, Parsed any](
	ctx context.Context,
	config PipelineConfig[ID, Parsed],
	day time.Time,
	ids []ID,
) error {
	if len(ids) == 0 {
		return nil
	}

	errcList := make([]<-chan error, RoutinesPerStage)
	generator := Generator[ID, Parsed](ctx, day, ids)

	dlStages := make([]<-chan *PipelineItem[ID, Parsed], RoutinesPerStage)
	downloadFn := DownloadStage(config)
	for i := 0; i < RoutinesPerStage; i++ {
		channel, errc := downloadFn(ctx, generator)
		dlStages[i] = channel
		errcList[i] = errc
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("panic in download consumer")
			}
		}()
		defer wg.Done()
		for result := range FanIn(ctx, dlStages...) {
			log.Info().Str("id", config.IDName(result.ID)).Msg("Downloaded")
		}
	}()

	if err := WaitForPipeline(errcList...); err != nil {
		return err
	}
	wg.Wait()
	return nil
}

// RunImportPipeline runs a full download+parse+import pipeline for a day
func RunImportPipeline[ID any, Parsed any](
	ctx context.Context,
	config PipelineConfig[ID, Parsed],
	day time.Time,
	ids []ID,
) error {
	if len(ids) == 0 {
		return nil
	}

	const stages = 3
	errcList := make([]<-chan error, RoutinesPerStage*stages)
	generator := Generator[ID, Parsed](ctx, day, ids)

	// Stage 1: Download
	dlStages := make([]<-chan *PipelineItem[ID, Parsed], RoutinesPerStage)
	downloadFn := DownloadStage(config)
	for i := 0; i < RoutinesPerStage; i++ {
		channel, errc := downloadFn(ctx, generator)
		dlStages[i] = channel
		errcList[i] = errc
	}

	// Stage 2: Parse
	parseStages := make([]<-chan *PipelineItem[ID, Parsed], RoutinesPerStage)
	parseFn := ParseStage(config)
	for i := 0; i < RoutinesPerStage; i++ {
		channel, errc := parseFn(ctx, dlStages[i])
		parseStages[i] = channel
		errcList[i+RoutinesPerStage] = errc
	}

	// Stage 3: Import
	importStages := make([]<-chan *PipelineItem[ID, Parsed], RoutinesPerStage)
	importFn := ImportStage(config)
	for i := 0; i < RoutinesPerStage; i++ {
		channel, errc := importFn(ctx, parseStages[i])
		importStages[i] = channel
		errcList[i+(RoutinesPerStage*2)] = errc
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("panic in import consumer")
			}
		}()
		defer wg.Done()
		for result := range FanIn(ctx, importStages...) {
			log.Info().Str("id", config.IDName(result.ID)).Msg("Imported")
		}
	}()

	if err := WaitForPipeline(errcList...); err != nil {
		return err
	}
	wg.Wait()
	return nil
}
