package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// IngestQueries is the database access StoreItems needs.
type IngestQueries interface {
	GetNewsArticleID(ctx context.Context, arg sqlcdb.GetNewsArticleIDParams) (int64, error)
	UpsertNewsArticle(ctx context.Context, arg sqlcdb.UpsertNewsArticleParams) (int64, error)
	GetLatestNewsArticleVersion(ctx context.Context, articleID int64) (sqlcdb.GetLatestNewsArticleVersionRow, error)
	InsertNewsArticleVersion(ctx context.Context, arg sqlcdb.InsertNewsArticleVersionParams) (int64, error)
}

// IngestResult counts what one fetch stored.
type IngestResult struct {
	Items       int `json:"items"`
	NewVersions int `json:"newVersions"`
	Unchanged   int `json:"unchanged"`
	// Skipped items were not news (a Yahoo player with no status who never
	// had one on record).
	Skipped int `json:"skipped"`
}

// StoreItems records a source's items. An item whose content matches its
// article's latest version only refreshes the article's last-seen time; a
// new or changed item becomes a new version, which processing then picks up.
// Re-running it with the same items changes nothing, so a retried fetch is
// safe.
func StoreItems(ctx context.Context, q IngestQueries, src Source, items []Item) (IngestResult, error) {
	result := IngestResult{Items: len(items)}
	for _, item := range items {
		stored, err := storeItem(ctx, q, src, item)
		if err != nil {
			return result, fmt.Errorf("store %s item %s: %w", src.ID, item.ExternalID, err)
		}
		switch stored {
		case storedNewVersion:
			result.NewVersions++
		case storedUnchanged:
			result.Unchanged++
		case storedSkipped:
			result.Skipped++
		}
	}
	return result, nil
}

type storeOutcome int

const (
	storedSkipped storeOutcome = iota
	storedUnchanged
	storedNewVersion
)

// firstVersion is the version number of an article's first content.
const firstVersion = 1

func storeItem(ctx context.Context, q IngestQueries, src Source, item Item) (storeOutcome, error) {
	if item.OnlyIfKnown {
		_, err := q.GetNewsArticleID(ctx, sqlcdb.GetNewsArticleIDParams{Publisher: src.Publisher, ExternalID: item.ExternalID})
		if errors.Is(err, pgx.ErrNoRows) {
			return storedSkipped, nil
		}
		if err != nil {
			return storedSkipped, err
		}
	}
	articleID, err := q.UpsertNewsArticle(ctx, sqlcdb.UpsertNewsArticleParams{
		Publisher: src.Publisher, ExternalID: item.ExternalID, SourceID: src.ID, Kind: string(src.Kind),
		Url: item.URL, FirstSeenAt: Timestamptz(item.RetrievedAt), SourceUpdatedAt: Timestamptz(item.UpdatedAt),
	})
	if err != nil {
		return storedSkipped, err
	}
	hash, err := item.ContentHash()
	if err != nil {
		return storedSkipped, err
	}
	version := int32(firstVersion)
	latest, err := q.GetLatestNewsArticleVersion(ctx, articleID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return storedSkipped, err
	case latest.ContentHash == hash:
		return storedUnchanged, nil
	default:
		version = latest.Version + 1
	}
	subjects, err := json.Marshal(item.Subjects)
	if err != nil {
		return storedSkipped, err
	}
	teamHints := item.TeamHints
	if teamHints == nil {
		teamHints = []string{}
	}
	_, err = q.InsertNewsArticleVersion(ctx, sqlcdb.InsertNewsArticleVersionParams{
		ArticleID: articleID, Version: version, ContentHash: hash,
		TitleFingerprint: item.TitleFingerprint(), TextFingerprint: item.TextFingerprint(),
		Title: item.Title, EvidenceText: item.Text, Body: item.Body, Author: item.Author, Url: item.URL,
		PublishedAt: Timestamptz(item.PublishedAt), SourceUpdatedAt: Timestamptz(item.UpdatedAt),
		RetrievedAt: Timestamptz(item.RetrievedAt), Subjects: subjects, TeamHints: teamHints,
		CategoryHint: string(item.CategoryHint),
	})
	if err != nil {
		return storedSkipped, err
	}
	return storedNewVersion, nil
}

// Timestamptz converts t, with the zero time as NULL.
func Timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// timeOf reads a nullable timestamp, NULL as the zero time.
func timeOf(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}
