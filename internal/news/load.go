package news

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ReportQueries is the database access the news report needs.
type ReportQueries interface {
	ListNewsFetchStates(ctx context.Context) ([]sqlcdb.NewsFetchState, error)
	ListRecentNewsIncidents(ctx context.Context, arg sqlcdb.ListRecentNewsIncidentsParams) ([]sqlcdb.NewsIncident, error)
	ListNewsIncidentsForPlayer(ctx context.Context, arg sqlcdb.ListNewsIncidentsForPlayerParams) ([]sqlcdb.NewsIncident, error)
	ListNewsIncidentEvidence(ctx context.Context, incidentIDs []int64) ([]sqlcdb.ListNewsIncidentEvidenceRow, error)
	ListNewsMentionIssues(ctx context.Context, arg sqlcdb.ListNewsMentionIssuesParams) ([]sqlcdb.ListNewsMentionIssuesRow, error)
}

// LoadFetchStates reads every recorded fetch state.
func LoadFetchStates(ctx context.Context, q ReportQueries) ([]FetchState, error) {
	rows, err := q.ListNewsFetchStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list news fetch states: %w", err)
	}
	states := make([]FetchState, 0, len(rows))
	for _, r := range rows {
		states = append(states, FetchStateFromRow(r))
	}
	return states, nil
}

// FetchStateFromRow converts a news_fetch_state row.
func FetchStateFromRow(r sqlcdb.NewsFetchState) FetchState {
	return FetchState{
		SourceID: r.SourceID, Scope: r.Scope,
		LastAttemptAt: timeOf(r.LastAttemptAt), LastSuccessAt: timeOf(r.LastSuccessAt),
		DataAsOf: timeOf(r.DataAsOf), LastFailureAt: timeOf(r.LastFailureAt),
		LastError: r.LastError, ConsecutiveFailures: int(r.ConsecutiveFailures),
		LastItems: int(r.LastItems), LastNewVersions: int(r.LastNewVersions),
	}
}

// LoadRecentIncidents reads up to limit incidents reported since the given
// time, newest first, with their evidence.
func LoadRecentIncidents(ctx context.Context, q ReportQueries, since time.Time, limit int) ([]Incident, error) {
	rows, err := q.ListRecentNewsIncidents(ctx, sqlcdb.ListRecentNewsIncidentsParams{
		LastReportedAt: Timestamptz(since), Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list recent news incidents: %w", err)
	}
	return withEvidence(ctx, q, rows)
}

// LoadPlayerNews reads every incident of a player (by NHL or Yahoo ID) and
// pairs it with the sources' coverage.
func LoadPlayerNews(ctx context.Context, q ReportQueries, player Identity, coverage []SourceCoverage) (PlayerNews, error) {
	rows, err := q.ListNewsIncidentsForPlayer(ctx, sqlcdb.ListNewsIncidentsForPlayerParams{
		NhlPlayerID:   pgtype.Int8{Int64: player.NHLPlayerID, Valid: player.NHLPlayerID != 0},
		YahooPlayerID: pgtype.Int4{Int32: int32(player.YahooPlayerID), Valid: player.YahooPlayerID != 0},
	})
	if err != nil {
		return PlayerNews{}, fmt.Errorf("list news incidents of %s: %w", player.Key(), err)
	}
	incidents, err := withEvidence(ctx, q, rows)
	if err != nil {
		return PlayerNews{}, err
	}
	return PlayerNews{Player: player, Incidents: incidents, Coverage: coverage}, nil
}

func withEvidence(ctx context.Context, q ReportQueries, rows []sqlcdb.NewsIncident) ([]Incident, error) {
	incidents := make([]Incident, 0, len(rows))
	index := make(map[int64]int, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		index[r.ID] = len(incidents)
		ids = append(ids, r.ID)
		incidents = append(incidents, Incident{
			ID: r.ID, NHLPlayerID: r.NhlPlayerID.Int64, YahooPlayerID: int(r.YahooPlayerID.Int32),
			PlayerName: r.PlayerName, Category: Category(r.Category),
			FirstReportedAt: timeOf(r.FirstReportedAt), LastReportedAt: timeOf(r.LastReportedAt),
		})
	}
	if len(ids) == 0 {
		return incidents, nil
	}
	evidence, err := q.ListNewsIncidentEvidence(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list news incident evidence: %w", err)
	}
	for _, e := range evidence {
		i := &incidents[index[e.IncidentID]]
		i.Evidence = append(i.Evidence, Evidence{
			Relation: Relation(e.Relation), Publisher: e.Publisher, Kind: Kind(e.Kind), SourceID: e.SourceID,
			Title: e.Title, Text: e.EvidenceText, Author: e.Author, URL: e.Url,
			PublishedAt: timeOf(e.PublishedAt), SourceUpdatedAt: timeOf(e.SourceUpdatedAt),
			RetrievedAt: timeOf(e.RetrievedAt), ReportedAt: timeOf(e.ReportedAt),
			Version: int(e.Version), LatestVersion: int(e.LatestVersion),
		})
	}
	return incidents, nil
}

// MentionIssue is a story subject that did not resolve to one player.
type MentionIssue struct {
	Mention     string
	Resolution  Resolution
	Method      Method
	Candidates  []Identity
	Title       string
	URL         string
	Publisher   string
	RetrievedAt time.Time
}

// LoadMentionIssues reads up to limit unresolved or ambiguous subjects of
// stories retrieved since the given time.
func LoadMentionIssues(ctx context.Context, q ReportQueries, since time.Time, limit int) ([]MentionIssue, error) {
	rows, err := q.ListNewsMentionIssues(ctx, sqlcdb.ListNewsMentionIssuesParams{
		RetrievedAt: Timestamptz(since), Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list news mention issues: %w", err)
	}
	issues := make([]MentionIssue, 0, len(rows))
	for _, r := range rows {
		var candidates []Identity
		if err := json.Unmarshal(r.Candidates, &candidates); err != nil {
			return nil, fmt.Errorf("decode candidates of mention %q: %w", r.Mention, err)
		}
		issues = append(issues, MentionIssue{
			Mention: r.Mention, Resolution: Resolution(r.Resolution), Method: Method(r.Method), Candidates: candidates,
			Title: r.Title, URL: r.Url, Publisher: r.Publisher, RetrievedAt: timeOf(r.RetrievedAt),
		})
	}
	return issues, nil
}
