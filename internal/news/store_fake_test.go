package news

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// fakeStore is an in-memory stand-in for the news tables implementing the
// queries of StoreItems, ProcessVersion and the report loaders with the same
// semantics as internal/sqlcdb/queries/news.sql.
type fakeStore struct {
	nextID    int64
	articles  []sqlcdb.NewsArticle
	versions  []sqlcdb.NewsArticleVersion
	mentions  map[int64][]sqlcdb.InsertNewsMentionParams
	incidents []sqlcdb.NewsIncident
	evidence  []sqlcdb.InsertNewsIncidentEvidenceParams
	states    []sqlcdb.NewsFetchState
}

func newFakeStore() *fakeStore {
	return &fakeStore{mentions: make(map[int64][]sqlcdb.InsertNewsMentionParams)}
}

func (f *fakeStore) id() int64 {
	f.nextID++
	return f.nextID
}

func (f *fakeStore) article(id int64) *sqlcdb.NewsArticle {
	for i := range f.articles {
		if f.articles[i].ID == id {
			return &f.articles[i]
		}
	}
	return nil
}

func (f *fakeStore) version(id int64) *sqlcdb.NewsArticleVersion {
	for i := range f.versions {
		if f.versions[i].ID == id {
			return &f.versions[i]
		}
	}
	return nil
}

func (f *fakeStore) GetNewsArticleID(_ context.Context, arg sqlcdb.GetNewsArticleIDParams) (int64, error) {
	for _, a := range f.articles {
		if a.Publisher == arg.Publisher && a.ExternalID == arg.ExternalID {
			return a.ID, nil
		}
	}
	return 0, pgx.ErrNoRows
}

func (f *fakeStore) UpsertNewsArticle(ctx context.Context, arg sqlcdb.UpsertNewsArticleParams) (int64, error) {
	if id, err := f.GetNewsArticleID(ctx, sqlcdb.GetNewsArticleIDParams{Publisher: arg.Publisher, ExternalID: arg.ExternalID}); err == nil {
		a := f.article(id)
		a.Url = arg.Url
		if arg.FirstSeenAt.Time.After(a.LastSeenAt.Time) {
			a.LastSeenAt = arg.FirstSeenAt
		}
		if arg.SourceUpdatedAt.Valid {
			a.SourceUpdatedAt = arg.SourceUpdatedAt
		}
		return id, nil
	}
	a := sqlcdb.NewsArticle{
		ID: f.id(), Publisher: arg.Publisher, ExternalID: arg.ExternalID, SourceID: arg.SourceID, Kind: arg.Kind,
		Url: arg.Url, FirstSeenAt: arg.FirstSeenAt, LastSeenAt: arg.FirstSeenAt, SourceUpdatedAt: arg.SourceUpdatedAt,
	}
	f.articles = append(f.articles, a)
	return a.ID, nil
}

func (f *fakeStore) GetLatestNewsArticleVersion(_ context.Context, articleID int64) (sqlcdb.GetLatestNewsArticleVersionRow, error) {
	var latest *sqlcdb.NewsArticleVersion
	for i := range f.versions {
		if v := &f.versions[i]; v.ArticleID == articleID && (latest == nil || v.Version > latest.Version) {
			latest = v
		}
	}
	if latest == nil {
		return sqlcdb.GetLatestNewsArticleVersionRow{}, pgx.ErrNoRows
	}
	return sqlcdb.GetLatestNewsArticleVersionRow{ID: latest.ID, Version: latest.Version, ContentHash: latest.ContentHash}, nil
}

func (f *fakeStore) GetLatestNewsArticleBody(ctx context.Context, arg sqlcdb.GetLatestNewsArticleBodyParams) (sqlcdb.GetLatestNewsArticleBodyRow, error) {
	id, err := f.GetNewsArticleID(ctx, sqlcdb.GetNewsArticleIDParams(arg))
	if err != nil {
		return sqlcdb.GetLatestNewsArticleBodyRow{}, err
	}
	latest, err := f.GetLatestNewsArticleVersion(ctx, id)
	if err != nil {
		return sqlcdb.GetLatestNewsArticleBodyRow{}, err
	}
	v := f.version(latest.ID)
	return sqlcdb.GetLatestNewsArticleBodyRow{SourceUpdatedAt: f.article(id).SourceUpdatedAt, Body: v.Body, Subjects: v.Subjects}, nil
}

func (f *fakeStore) latestVersion(articleID int64) int32 {
	row, _ := f.GetLatestNewsArticleVersion(context.Background(), articleID)
	return row.Version
}

func (f *fakeStore) InsertNewsArticleVersion(_ context.Context, arg sqlcdb.InsertNewsArticleVersionParams) (int64, error) {
	v := sqlcdb.NewsArticleVersion{
		ID: f.id(), ArticleID: arg.ArticleID, Version: arg.Version, ContentHash: arg.ContentHash,
		TitleFingerprint: arg.TitleFingerprint, TextFingerprint: arg.TextFingerprint, Title: arg.Title,
		EvidenceText: arg.EvidenceText, Body: arg.Body, Author: arg.Author, Url: arg.Url, PublishedAt: arg.PublishedAt,
		SourceUpdatedAt: arg.SourceUpdatedAt, RetrievedAt: arg.RetrievedAt, Subjects: arg.Subjects,
		TeamHints: arg.TeamHints, CategoryHint: arg.CategoryHint,
	}
	f.versions = append(f.versions, v)
	return v.ID, nil
}

// unprocessed mirrors ListUnprocessedNewsVersions.
func (f *fakeStore) unprocessed() []sqlcdb.ListUnprocessedNewsVersionsRow {
	var rows []sqlcdb.ListUnprocessedNewsVersionsRow
	for _, v := range f.versions {
		if v.ProcessedAt.Valid {
			continue
		}
		a := f.article(v.ArticleID)
		rows = append(rows, sqlcdb.ListUnprocessedNewsVersionsRow{
			ID: v.ID, ArticleID: v.ArticleID, Version: v.Version, ContentHash: v.ContentHash,
			TitleFingerprint: v.TitleFingerprint, TextFingerprint: v.TextFingerprint, Title: v.Title,
			EvidenceText: v.EvidenceText, Author: v.Author, Url: v.Url, PublishedAt: v.PublishedAt,
			SourceUpdatedAt: v.SourceUpdatedAt, RetrievedAt: v.RetrievedAt, Subjects: v.Subjects,
			TeamHints: v.TeamHints, CategoryHint: v.CategoryHint, Body: v.Body,
			Publisher: a.Publisher, Kind: a.Kind, SourceID: a.SourceID,
		})
	}
	return rows
}

func (f *fakeStore) DeleteNewsMentions(_ context.Context, versionID int64) error {
	delete(f.mentions, versionID)
	return nil
}

func (f *fakeStore) InsertNewsMention(_ context.Context, arg sqlcdb.InsertNewsMentionParams) error {
	f.mentions[arg.VersionID] = append(f.mentions[arg.VersionID], arg)
	return nil
}

func (f *fakeStore) ListNewsIncidentCandidates(_ context.Context, arg sqlcdb.ListNewsIncidentCandidatesParams) ([]sqlcdb.NewsIncident, error) {
	var out []sqlcdb.NewsIncident
	for _, inc := range f.incidents {
		samePlayer := (arg.NhlPlayerID.Valid && inc.NhlPlayerID.Valid && inc.NhlPlayerID.Int64 == arg.NhlPlayerID.Int64) ||
			(arg.YahooPlayerID.Valid && inc.YahooPlayerID.Valid && inc.YahooPlayerID.Int32 == arg.YahooPlayerID.Int32)
		if samePlayer && !inc.FirstReportedAt.Time.After(arg.Latest.Time) && !inc.LastReportedAt.Time.Before(arg.Earliest.Time) {
			out = append(out, inc)
		}
	}
	slices.SortFunc(out, func(a, b sqlcdb.NewsIncident) int { return b.LastReportedAt.Time.Compare(a.LastReportedAt.Time) })
	return out, nil
}

func (f *fakeStore) ListNewsIncidentEvidenceKeys(_ context.Context, ids []int64) ([]sqlcdb.ListNewsIncidentEvidenceKeysRow, error) {
	var out []sqlcdb.ListNewsIncidentEvidenceKeysRow
	for _, e := range f.evidence {
		if slices.Contains(ids, e.IncidentID) {
			v := f.version(e.VersionID)
			out = append(out, sqlcdb.ListNewsIncidentEvidenceKeysRow{
				IncidentID: e.IncidentID, VersionID: e.VersionID, ArticleID: e.ArticleID, Publisher: e.Publisher,
				TitleFingerprint: v.TitleFingerprint, TextFingerprint: v.TextFingerprint,
			})
		}
	}
	return out, nil
}

func (f *fakeStore) CreateNewsIncident(_ context.Context, arg sqlcdb.CreateNewsIncidentParams) (int64, error) {
	inc := sqlcdb.NewsIncident{
		ID: f.id(), NhlPlayerID: arg.NhlPlayerID, YahooPlayerID: arg.YahooPlayerID, PlayerName: arg.PlayerName,
		Category: arg.Category, FirstReportedAt: arg.FirstReportedAt, LastReportedAt: arg.FirstReportedAt,
	}
	f.incidents = append(f.incidents, inc)
	return inc.ID, nil
}

func (f *fakeStore) ExtendNewsIncident(_ context.Context, arg sqlcdb.ExtendNewsIncidentParams) error {
	for i := range f.incidents {
		inc := &f.incidents[i]
		if inc.ID != arg.ID {
			continue
		}
		if arg.ReportedAt.Time.Before(inc.FirstReportedAt.Time) {
			inc.FirstReportedAt = arg.ReportedAt
		}
		if arg.ReportedAt.Time.After(inc.LastReportedAt.Time) {
			inc.LastReportedAt = arg.ReportedAt
		}
		if !inc.NhlPlayerID.Valid {
			inc.NhlPlayerID = arg.NhlPlayerID
		}
		if !inc.YahooPlayerID.Valid {
			inc.YahooPlayerID = arg.YahooPlayerID
		}
	}
	return nil
}

func (f *fakeStore) InsertNewsIncidentEvidence(_ context.Context, arg sqlcdb.InsertNewsIncidentEvidenceParams) error {
	for _, e := range f.evidence {
		if e.IncidentID == arg.IncidentID && e.VersionID == arg.VersionID {
			return nil
		}
	}
	f.evidence = append(f.evidence, arg)
	return nil
}

func (f *fakeStore) MarkNewsVersionProcessed(_ context.Context, arg sqlcdb.MarkNewsVersionProcessedParams) error {
	f.version(arg.ID).ProcessedAt = arg.ProcessedAt
	return nil
}

func (f *fakeStore) ListNewsFetchStates(context.Context) ([]sqlcdb.NewsFetchState, error) {
	return f.states, nil
}

func (f *fakeStore) ListRecentNewsIncidents(_ context.Context, arg sqlcdb.ListRecentNewsIncidentsParams) ([]sqlcdb.NewsIncident, error) {
	var out []sqlcdb.NewsIncident
	for _, inc := range f.incidents {
		if !inc.LastReportedAt.Time.Before(arg.LastReportedAt.Time) {
			out = append(out, inc)
		}
	}
	slices.SortFunc(out, func(a, b sqlcdb.NewsIncident) int { return b.LastReportedAt.Time.Compare(a.LastReportedAt.Time) })
	return out[:min(len(out), int(arg.Limit))], nil
}

func (f *fakeStore) ListNewsIncidentsForPlayer(_ context.Context, arg sqlcdb.ListNewsIncidentsForPlayerParams) ([]sqlcdb.NewsIncident, error) {
	var out []sqlcdb.NewsIncident
	for _, inc := range f.incidents {
		if (arg.NhlPlayerID.Valid && inc.NhlPlayerID.Int64 == arg.NhlPlayerID.Int64) ||
			(arg.YahooPlayerID.Valid && inc.YahooPlayerID.Int32 == arg.YahooPlayerID.Int32) {
			out = append(out, inc)
		}
	}
	slices.SortFunc(out, func(a, b sqlcdb.NewsIncident) int { return a.FirstReportedAt.Time.Compare(b.FirstReportedAt.Time) })
	return out, nil
}

func (f *fakeStore) ListNewsIncidentEvidence(_ context.Context, ids []int64) ([]sqlcdb.ListNewsIncidentEvidenceRow, error) {
	var out []sqlcdb.ListNewsIncidentEvidenceRow
	for _, e := range f.evidence {
		if !slices.Contains(ids, e.IncidentID) {
			continue
		}
		v := f.version(e.VersionID)
		out = append(out, sqlcdb.ListNewsIncidentEvidenceRow{
			IncidentID: e.IncidentID, Relation: e.Relation, ReportedAt: e.ReportedAt, Publisher: e.Publisher,
			Kind: e.Kind, RelatedVersionID: e.RelatedVersionID, VersionID: v.ID, Version: v.Version, Title: v.Title,
			EvidenceText: v.EvidenceText, Author: v.Author, Url: v.Url, PublishedAt: v.PublishedAt,
			SourceUpdatedAt: v.SourceUpdatedAt, RetrievedAt: v.RetrievedAt,
			SourceID: f.article(v.ArticleID).SourceID, LatestVersion: f.latestVersion(v.ArticleID),
		})
	}
	slices.SortStableFunc(out, func(a, b sqlcdb.ListNewsIncidentEvidenceRow) int {
		if a.IncidentID != b.IncidentID {
			return int(a.IncidentID - b.IncidentID)
		}
		return a.ReportedAt.Time.Compare(b.ReportedAt.Time)
	})
	return out, nil
}

func (f *fakeStore) ListNewsMentionIssues(context.Context, sqlcdb.ListNewsMentionIssuesParams) ([]sqlcdb.ListNewsMentionIssuesRow, error) {
	var out []sqlcdb.ListNewsMentionIssuesRow
	for _, v := range f.versions {
		for _, m := range f.mentions[v.ID] {
			if m.Role == string(RoleSubject) && m.Resolution != string(ResolutionResolved) {
				out = append(out, sqlcdb.ListNewsMentionIssuesRow{
					Mention: m.Mention, Resolution: m.Resolution, Method: m.Method, Candidates: m.Candidates,
					Title: v.Title, Url: v.Url, RetrievedAt: v.RetrievedAt, Publisher: f.article(v.ArticleID).Publisher,
				})
			}
		}
	}
	return out, nil
}

// incidentFor returns the incidents of a player by NHL ID.
func (f *fakeStore) incidentsOf(nhlID int64) []sqlcdb.NewsIncident {
	var out []sqlcdb.NewsIncident
	for _, inc := range f.incidents {
		if inc.NhlPlayerID.Valid && inc.NhlPlayerID.Int64 == nhlID {
			out = append(out, inc)
		}
	}
	return out
}

func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
