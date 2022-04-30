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
	log "github.com/sirupsen/logrus"
)

/**
 * Ensure there's a file in github for the list of games for a given day
 */
func EnsureRecentGamesList(ctx context.Context, yfh *core.YFH, date time.Time) (*core.GithubFile, error) {
	log.Debugf("EnsureRecentGameList date: %4d-%02d-%02d", date.Year(), date.Month(), date.Day())
	// fetch the files in github for the list of games for that date
	gitfiles, err := yfh.Github.FindGamesList(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("EnsureRecentGamesList: %w", err)
	}
	log.Debugf("Found %s for %d-%02d-%02d in github", english.Plural(len(gitfiles), "game list", ""), date.Year(), date.Month(), date.Day())
	if len(gitfiles) == 0 {
		// download it if none are found then add it in github
		content, err := Download(ctx, yfh, core.GamesListURL(date))
		if err != nil {
			return nil, err
		}
		if err := yfh.Github.CreateGamesList(ctx, date, content); err != nil {
			return nil, err
		}
		// get the gitfile for what we just created, it's from the cache so it's quick
		gitfiles, err = yfh.Github.FindGamesList(ctx, date)
		if err != nil {
			return nil, err
		}
		if len(gitfiles) == 0 {
			return nil, fmt.Errorf("should have found at least one file for games list %d-%02d-%02d", date.Year(), date.Month(), date.Day())
		}
	}
	// return the latest
	return gitfiles[0], nil
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
