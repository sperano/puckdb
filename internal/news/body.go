package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// BodyQueries is the database access AttachBodies needs.
type BodyQueries interface {
	GetLatestNewsArticleBody(ctx context.Context, arg sqlcdb.GetLatestNewsArticleBodyParams) (sqlcdb.GetLatestNewsArticleBodyRow, error)
}

// PageFetcher downloads one story page.
type PageFetcher func(ctx context.Context, url string) ([]byte, error)

// MaxBodyDeferral is how long after its last source update a story whose
// page keeps failing is deferred. Past it the story is stored with its
// summary only, so a flaky page cannot keep an official report out of the
// database; a later update to the story downloads the page again.
const MaxBodyDeferral = 6 * time.Hour

// BodyOptions says how AttachBodies downloads story pages.
type BodyOptions struct {
	// Fetch downloads a story page.
	Fetch PageFetcher
	// Deadline is when to stop downloading pages; the stories left are
	// deferred to the next refresh. Zero means no deadline.
	Deadline time.Time
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (o BodyOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o BodyOptions) pastDeadline() bool {
	return !o.Deadline.IsZero() && !o.now().Before(o.Deadline)
}

// deferrable reports whether a story whose page failed transiently may wait
// for the next refresh: its latest source time is recent enough.
func (o BodyOptions) deferrable(item Item) bool {
	latest := item.PublishedAt
	if item.UpdatedAt.After(latest) {
		latest = item.UpdatedAt
	}
	return !latest.IsZero() && o.now().Sub(latest) < MaxBodyDeferral
}

// BodyResult counts how AttachBodies got the stories' full text.
type BodyResult struct {
	// Fetched stories had their page downloaded.
	Fetched int `json:"fetched"`
	// Reused stories had not been updated since their body was stored.
	Reused int `json:"reused"`
	// Unavailable stories have no readable page (gone, forbidden, too large,
	// unparseable, or failing past MaxBodyDeferral) and are stored with
	// their summary only.
	Unavailable int `json:"unavailable"`
	// Deferred stories were left for the next refresh: their page could not
	// be downloaded now (network error, 5xx, rate limit) and the story is
	// recent (MaxBodyDeferral), or the time budget ran out.
	Deferred int `json:"deferred"`
}

// Add accumulates another result.
func (r *BodyResult) Add(other BodyResult) {
	r.Fetched += other.Fetched
	r.Reused += other.Reused
	r.Unavailable += other.Unavailable
	r.Deferred += other.Deferred
}

// AttachBodies fills in the full text of a fetch_body source's items and the
// players their bodies link. A story is downloaded only when it is new or
// its source update time moved since it was last seen; otherwise the stored
// body is reused, so an unchanged story never becomes a new version for
// lack of one. Deferred items are left out of the returned items: storing
// them without a body would record a version that the next refresh replaces.
func AttachBodies(ctx context.Context, q BodyQueries, src Source, items []Item, opts BodyOptions) ([]Item, BodyResult, error) {
	var result BodyResult
	out := make([]Item, 0, len(items))
	for _, item := range items {
		if item.BodyURL == "" {
			out = append(out, item)
			continue
		}
		reused, ok, err := reuseBody(ctx, q, src, item)
		if err != nil {
			return nil, result, err
		}
		if ok {
			result.Reused++
			out = append(out, reused)
			continue
		}
		if opts.pastDeadline() {
			result.Deferred++
			continue
		}
		fetched, outcome, err := fetchBody(ctx, opts.Fetch, item)
		if err != nil {
			return nil, result, err
		}
		switch outcome {
		case bodyFetched:
			result.Fetched++
		case bodyUnavailable:
			result.Unavailable++
		case bodyDeferred:
			if opts.deferrable(item) {
				result.Deferred++
				continue
			}
			result.Unavailable++
		}
		out = append(out, fetched)
	}
	return out, result, nil
}

// reuseBody gives item the body and body subjects of its article's latest
// version when the source has not updated the story since.
func reuseBody(ctx context.Context, q BodyQueries, src Source, item Item) (Item, bool, error) {
	if item.UpdatedAt.IsZero() {
		return item, false, nil
	}
	row, err := q.GetLatestNewsArticleBody(ctx, sqlcdb.GetLatestNewsArticleBodyParams{
		Publisher: src.Publisher, ExternalID: item.ExternalID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, false, fmt.Errorf("load stored body of %s: %w", item.ExternalID, err)
	}
	if !row.SourceUpdatedAt.Valid || !row.SourceUpdatedAt.Time.Equal(item.UpdatedAt) {
		return item, false, nil
	}
	var stored []Subject
	if err := json.Unmarshal(row.Subjects, &stored); err != nil {
		// Unreadable stored subjects: download the page again rather than
		// fail the fetch on every retry.
		return item, false, nil
	}
	var inBody []Subject
	for _, s := range stored {
		if s.InBody {
			inBody = append(inBody, s)
		}
	}
	return withBody(item, row.Body, inBody), true, nil
}

type bodyOutcome int

const (
	bodyFetched bodyOutcome = iota
	bodyUnavailable
	bodyDeferred
)

// fetchBody downloads and reads the item's story page. Only a cancelled
// context is an error; a page that cannot be read now or ever is reported
// as an outcome.
func fetchBody(ctx context.Context, fetch PageFetcher, item Item) (Item, bodyOutcome, error) {
	page, err := fetch(ctx, item.BodyURL)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return item, bodyDeferred, ctxErr
	}
	if err != nil {
		if permanentFetchError(err) {
			return item, bodyUnavailable, nil
		}
		return item, bodyDeferred, nil
	}
	text, subjects, err := ParseNHLStoryBody(page)
	if err != nil {
		return item, bodyUnavailable, nil
	}
	return withBody(item, CleanBody(text, MaxBodyRunes), subjects), bodyFetched, nil
}

// permanentFetchError reports whether retrying the download cannot help.
func permanentFetchError(err error) bool {
	if errors.Is(err, ErrFeedTooLarge) {
		return true
	}
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && !httpErr.Retryable()
}

// withBody sets the item's body and adds the players it links, keeping a
// player the story is tagged with as tagged.
func withBody(item Item, body string, bodySubjects []Subject) Item {
	item.Body = body
	if joinFields(body) == item.Text {
		item.Body = ""
	}
	tagged := make([]Subject, 0, len(item.Subjects)+len(bodySubjects))
	for _, s := range item.Subjects {
		if !s.InBody {
			tagged = append(tagged, s)
		}
	}
	for _, s := range bodySubjects {
		tagged = appendSubject(tagged, s)
	}
	item.Subjects = sortSubjects(tagged)
	return item
}
