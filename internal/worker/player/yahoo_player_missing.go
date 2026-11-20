package player

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
)

// errYahooPlayerPageMissing reports a player page whose 404 was recorded
// less than missingYahooPlayerPageTTL ago, so it is not downloaded.
var errYahooPlayerPageMissing = errors.New("player page answered 404")

// missingYahooPlayerPageTTL is how long a recorded 404 of a Yahoo Sports
// player page is trusted before the page is asked for again: a retired
// player's page stays gone, but Yahoo has answered intermittent 404s for
// valid pages (httpx.HTTPError.IsNonRetryableClientError).
const missingYahooPlayerPageTTL = 30 * 24 * time.Hour

// recentlyMissing reports whether id's page has a 404 recorded within
// missingYahooPlayerPageTTL. A marker that cannot be read or parsed, or
// dated after now (a clock moved back, a bad timestamp), counts as none, so
// the page is asked for again.
func (a *Activities) recentlyMissing(ctx context.Context, id store.YahooPlayerID) bool {
	marker := resource.MissingYahooPlayer{PlayerID: id}
	content, err := resource.ReadParsed(ctx, a.Storage, marker)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Warn().Err(err).Str("path", marker.Path()).Msg("Missing Yahoo player marker unreadable, asking for the page again")
		}
		return false
	}
	age := a.now().Sub(content.RecordedAt)
	return age >= 0 && age < missingYahooPlayerPageTTL
}

// recordIfMissing records a 404 marker for id when err is the download's
// 404. A write failure is logged: the page is then asked for again next time.
func (a *Activities) recordIfMissing(ctx context.Context, id store.YahooPlayerID, err error) {
	var httpErr *httpx.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		return
	}
	marker := resource.MissingYahooPlayer{PlayerID: id}
	content := resource.MissingYahooPlayerContent{RecordedAt: a.now()}
	if writeErr := resource.WriteParsed(ctx, a.Storage, marker, content); writeErr != nil {
		log.Warn().Err(writeErr).Str("path", marker.Path()).Msg("Failed to record missing Yahoo player page")
	}
}

// now is the Activities' clock: Now when set, else time.Now.
func (a *Activities) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
