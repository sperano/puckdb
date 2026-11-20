package player

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
)

// errNoYahooPlayerPages reports a player page that is not stored when the
// Activities have no YahooPlayerPages downloader.
var errNoYahooPlayerPages = errors.New("no Yahoo player page downloader")

// addYahooBirthDates gives the Yahoo pool entries the birth date matching
// needs for landing: the game player lists carry none, so for each name
// match the jersey step leaves tied (matching.BirthDatesNeeded) the player's
// Yahoo Sports page is read, from storage or Valkey when stored, downloaded
// otherwise, and its birth date set on the batch's pool entry. A page whose
// 404 was recorded recently (resource.MissingYahooPlayer) is not asked for
// and is counted apart. A page that cannot be had is logged and counted;
// the match then runs without that date, as it did before birth dates were fetched. The batch remembers it
// (out.unavailableBirthDates), so a later landing tied on the same Yahoo ID
// does not request it again; the next batch or run tries the page again.
// It returns ctx's error once ctx is done, so a cancelled activity stops
// instead of matching and upserting without the dates.
func (a *Activities) addYahooBirthDates(
	ctx context.Context,
	landing *nhl.PlayerLanding,
	nhlBirthDate time.Time,
	pool map[store.YahooPlayerID]*store.YahooPlayer,
	out *batchOutcome,
) error {
	result := &out.result
	for _, id := range matching.BirthDatesNeeded(landing, nhlBirthDate, pool) {
		if _, failed := out.unavailableBirthDates[id]; failed {
			continue
		}
		birth, downloaded, err := a.yahooBirthDate(ctx, id)
		if downloaded {
			result.BirthDatePagesDownloaded++
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("birth date of Yahoo player %d: %w", id, ctxErr)
		}
		if err != nil {
			out.birthDateUnavailable(landing, id, err)
			continue
		}
		pool[id].BirthDate = birth
	}
	return nil
}

// birthDateUnavailable records that id's birth date page cannot be had in
// this batch, counting a recorded 404 apart from a failure.
func (o *batchOutcome) birthDateUnavailable(landing *nhl.PlayerLanding, id store.YahooPlayerID, err error) {
	if o.unavailableBirthDates == nil {
		o.unavailableBirthDates = map[store.YahooPlayerID]struct{}{}
	}
	o.unavailableBirthDates[id] = struct{}{}
	if errors.Is(err, errYahooPlayerPageMissing) {
		o.result.BirthDatePagesMissing++
		return
	}
	o.result.BirthDatePagesFailed++
	log.Warn().Err(err).
		Int64("nhl_id", landing.PlayerID.Int64()).
		Int("yahoo_id", int(id)).
		Msg("Yahoo player birth date unavailable")
}

// yahooBirthDate returns the birth date on id's Yahoo Sports page (zero when
// the page shows none) and whether the page was downloaded, i.e. whether the
// downloader returned a body, even one that then fails to parse or to be
// stored. A stored page is
// read through the gob cache and never downloaded again (a birth date does
// not change), unless it no longer parses; a downloaded page is stored and
// cached only once it parses. A page with no stored copy whose 404 was
// recorded within missingYahooPlayerPageTTL returns errYahooPlayerPageMissing
// without a download, counted as a missing download; a download answering
// 404 records it.
func (a *Activities) yahooBirthDate(ctx context.Context, id store.YahooPlayerID) (time.Time, bool, error) {
	page := resource.YahooPlayer{PlayerID: id}
	raw, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, page)
	switch {
	case err == nil:
		player, parseErr := store.ParseYahooPlayerHTML(id, raw)
		if parseErr == nil {
			metrics.IncDownload(page.Type(), metrics.ResultHit)
			return player.BirthDate, false, nil
		}
		log.Warn().Err(parseErr).Str("path", page.Path()).Msg("Stored Yahoo player page does not parse, downloading it again")
	case errors.Is(err, os.ErrNotExist):
	default:
		metrics.IncDownload(page.Type(), metrics.ResultError)
		return time.Time{}, false, fmt.Errorf("read %s: %w", page.Path(), err)
	}
	if a.recentlyMissing(ctx, id) {
		metrics.IncDownload(page.Type(), metrics.ResultMissing)
		return time.Time{}, false, fmt.Errorf("%s: %w", page.URL(), errYahooPlayerPageMissing)
	}
	player, downloaded, err := a.downloadYahooPlayerPage(ctx, page)
	if err != nil {
		a.recordIfMissing(ctx, id, err)
		metrics.IncDownload(page.Type(), metrics.ResultError)
		return time.Time{}, downloaded, err
	}
	metrics.IncDownload(page.Type(), metrics.ResultMiss)
	return player.BirthDate, true, nil
}

// downloadYahooPlayerPage downloads page, and stores and caches it once it
// parses, so a bad response never becomes a stored page. Storage is the
// source of truth: a Valkey write failure is logged, not returned. The bool
// reports whether the downloader returned a body.
func (a *Activities) downloadYahooPlayerPage(ctx context.Context, page resource.YahooPlayer) (*store.YahooPlayer, bool, error) {
	if a.YahooPlayerPages == nil {
		return nil, false, fmt.Errorf("download %s: %w", page.URL(), errNoYahooPlayerPages)
	}
	raw, err := a.YahooPlayerPages(ctx, page.URL())
	if err != nil {
		return nil, false, fmt.Errorf("download %s: %w", page.URL(), err)
	}
	player, err := store.ParseYahooPlayerHTML(page.PlayerID, raw)
	if err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", page.URL(), err)
	}
	if err := resource.Write(ctx, a.Storage, page, raw); err != nil {
		return nil, true, fmt.Errorf("save %s: %w", page.Path(), err)
	}
	if err := a.GobCache.SetParsed(ctx, page, raw); err != nil && !errors.Is(err, cache.ErrNilCache) {
		log.Warn().Err(err).Str("path", page.Path()).Msg("gob cache write failed after Yahoo player page download; continuing")
	}
	return player, true, nil
}
