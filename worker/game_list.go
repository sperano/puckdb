package worker

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/gocolly/colly"
	"github.com/rs/zerolog/log"
)

/**
 * Ensure there's a file for the list of games for a given day
 */
func EnsureRecentGamesList(ctx context.Context, yfh *core.YFH, date time.Time) (*core.LocalFile, error) {
	log.Debug().Str("date", getDateStr(date)).Msg("EnsureRecentGameList")
	// find the files for the list of games for that date
	files, err := yfh.Local.FindGamesList(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("EnsureRecentGamesList: %w", err)
	}
	log.Debug().Str("date", getDateStr(date)).Msgf("Found %s", english.Plural(len(files), "game list", ""))
	if len(files) == 0 {
		// download it if none are found then save it locally
		content, err := Download(ctx, yfh, core.GamesListURL(date))
		if err != nil {
			return nil, err
		}
		if err := yfh.Local.CreateGamesList(ctx, date, content); err != nil {
			return nil, err
		}
		files, err = yfh.Local.FindGamesList(ctx, date)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("should have found at least one file for games list %d-%02d-%02d", date.Year(), date.Month(), date.Day())
		}
	}
	// return the latest
	return files[0], nil
}

/**
 * Parse the game list file and send the game urls on a channel
 */
func GameListGenerator(filename string) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		col := colly.NewCollector()
		t := &http.Transport{}
		t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
		col.WithTransport(t)
		col.OnHTML("div[class=scoreboard] a[href]", func(e *colly.HTMLElement) {
			url := e.Attr("href")
			// if the link starts with /nhl/, it's a game
			if strings.HasPrefix(url, "/nhl/") {
				out <- url
			}
		})
		col.Visit(fmt.Sprintf("file://%s", filename))
		col.Wait()
	}()
	return out
}
