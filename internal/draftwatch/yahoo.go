package draftwatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
)

const (
	draftStatusPreDraft  = "predraft"
	draftStatusPostDraft = "postdraft"
)

// PollResult is one pair of force-refreshed Yahoo status/draft observations.
type PollResult struct {
	Snapshot      draftsession.Snapshot
	DraftStatus   string
	DraftComplete bool
	SnapshotHash  string
	Duration      time.Duration
	PolledAt      time.Time
}

// YahooSource force-refreshes only the mutable league status and draft result
// resources. Fetcher.Refresh replaces both filesystem and Redis entries.
type YahooSource struct {
	Fetcher yahoo.Fetcher
	Now     func() time.Time
}

// NewYahooSource builds a source from the cache and authenticated downloader.
func NewYahooSource(storage store.Storage, gobCache *cache.GobCache, download shared.Downloader) YahooSource {
	return YahooSource{Fetcher: yahoo.Fetcher{Storage: storage, GobCache: gobCache, Download: download}, Now: time.Now}
}

// Poll fetches status before results so empty-result authority can be judged
// from the same poll. An empty response only resets during explicit predraft.
func (s YahooSource) Poll(ctx context.Context, id Identity) (PollResult, error) {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	started := now().UTC()
	statusResource := resource.League{Season: id.Season, LeagueID: id.LeagueID, GameKey: id.GameKey}
	status, err := s.Fetcher.RefreshCoherent(ctx, statusResource)
	if err != nil {
		return PollResult{}, fmt.Errorf("refresh Yahoo draft status: %w", err)
	}
	if err := statusResource.Validate(status); err != nil {
		return PollResult{}, fmt.Errorf("validate Yahoo draft status: %w", err)
	}
	resultsResource := resource.DraftResults{Season: id.Season, LeagueID: id.LeagueID, GameKey: id.GameKey}
	results, err := s.Fetcher.RefreshCoherent(ctx, resultsResource)
	if err != nil {
		return PollResult{}, fmt.Errorf("refresh Yahoo draft results: %w", err)
	}

	statusValue := normalizeDraftStatus(status.League.DraftStatus)
	snapshot := buildSnapshot(id, results, statusValue)
	hash, err := hashSnapshot(snapshot)
	if err != nil {
		return PollResult{}, err
	}
	finished := now().UTC()
	return PollResult{
		Snapshot: snapshot, DraftStatus: statusValue,
		DraftComplete: statusValue == draftStatusPostDraft,
		SnapshotHash:  hash, Duration: finished.Sub(started), PolledAt: finished,
	}, nil
}

func buildSnapshot(id Identity, content *store.FantasyContent, status string) draftsession.Snapshot {
	results := content.League.DraftResults
	observed := make([]draftsession.ObservedPick, 0, len(results.Slice))
	for _, result := range results.Slice {
		row := draftsession.ObservedPick{Key: draftsession.PickKey{Round: result.Round, Pick: result.Pick}}
		teamID, teamErr := ParseTeamKey(result.TeamKey, id)
		playerID, playerErr := ParsePlayerKey(result.PlayerKey, id)
		switch {
		case teamErr != nil:
			row.Issue = teamErr.Error()
		case playerErr != nil:
			row.Issue = playerErr.Error()
		default:
			row.TeamID = teamID
			row.PlayerID = playerID
			if result.Cost > 0 {
				cost := result.Cost
				row.Cost = &cost
			}
		}
		observed = append(observed, row)
	}
	// A positive response can safely add/correct valid rows even when its
	// count is incomplete. Empty omission is destructive only when Yahoo also
	// says the league is predraft and explicitly supplies count=0.
	authoritative := len(results.Slice) > 0 ||
		(status == draftStatusPreDraft && results.HasCount && results.Count == 0)
	return draftsession.Snapshot{
		Authoritative: authoritative, HasExpectedCount: results.HasCount,
		ExpectedCount: results.Count, RawCount: len(results.Slice), Picks: observed,
	}
}

func normalizeDraftStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func hashSnapshot(snapshot draftsession.Snapshot) (string, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal Yahoo draft snapshot: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ErrorClass is a stable capability-report class for a failed Yahoo poll,
// stored in draft_session_observations.error_class. It never carries token
// details.
type ErrorClass string

const (
	// ErrorClassNone is the class of a successful poll.
	ErrorClassNone           ErrorClass = ""
	ErrorClassAuthentication ErrorClass = "authentication"
	// ErrorClassRateLimited is HTTP 429, kept apart so watch backoff can
	// react to throttling.
	ErrorClassRateLimited ErrorClass = "rate_limited"
	ErrorClassUpstream    ErrorClass = "upstream"
	ErrorClassHTTP        ErrorClass = "http"
	ErrorClassCanceled    ErrorClass = "canceled"
	ErrorClassTransport   ErrorClass = "transport"
)

// ClassifyError returns the capability-report class of a poll error.
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ErrorClassNone
	}
	var httpErr *httpx.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return ErrorClassAuthentication
		case http.StatusTooManyRequests:
			return ErrorClassRateLimited
		default:
			if httpErr.StatusCode >= http.StatusInternalServerError {
				return ErrorClassUpstream
			}
			return ErrorClassHTTP
		}
	}
	var tokenErr *cache.OAuth2TokenMissingError
	if errors.As(err, &tokenErr) {
		return ErrorClassAuthentication
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorClassCanceled
	}
	return ErrorClassTransport
}
