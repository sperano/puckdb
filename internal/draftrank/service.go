package draftrank

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
)

// DefaultSnapshotCacheSize is how many decoded snapshots a Service keeps.
// Snapshots are immutable, so a cached one never goes stale; only the
// question of which one is latest is asked of the store on every request.
const DefaultSnapshotCacheSize = 8

// Reader is the storage the read path needs.
type Reader interface {
	FindLeagueKey(ctx context.Context, leagueKey string) (season, leagueID int, err error)
	LeagueRules(ctx context.Context, season, leagueID int) (*draft.Snapshot, error)
	ListLeagueRules(ctx context.Context, season int) ([]draft.Snapshot, error)
	LatestSnapshotID(ctx context.Context, season, leagueID int) (uuid.UUID, error)
	SnapshotInfo(ctx context.Context, id uuid.UUID) (*SnapshotInfo, error)
	LoadSnapshot(ctx context.Context, id uuid.UUID) (*Snapshot, error)
	LatestRefresh(ctx context.Context, season, leagueID int) (*Refresh, error)
	OverrideChanges(ctx context.Context, leagueKey string, since, now time.Time) (int64, error)
}

// ServiceOptions configure a Service. StaleAfter is how old a snapshot or
// player pool may get before it is reported stale.
type ServiceOptions struct {
	StaleAfter time.Duration
	Now        func() time.Time
	CacheSize  int
}

// Service serves rankings, league summaries and overrides. It reads stored
// snapshots only: it never ranks, adjusts or calls an LLM.
type Service struct {
	reader     Reader
	overrides  OverrideStore
	now        func() time.Time
	staleAfter time.Duration
	cache      *snapshotCache
}

// NewService returns a Service. overrides may be nil when override access
// is not needed (the CLI export).
func NewService(reader Reader, overrides OverrideStore, opts ServiceOptions) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.CacheSize <= 0 {
		opts.CacheSize = DefaultSnapshotCacheSize
	}
	return &Service{reader: reader, overrides: overrides, now: opts.Now, staleAfter: opts.StaleAfter, cache: newSnapshotCache(opts.CacheSize)}
}

// Page is one league's rankings response: the league, whether a ranking is
// available, the snapshot served (with its versions and freshness), the
// latest refresh attempt, every issue, and the requested rows.
type Page struct {
	League           League        `json:"league"`
	Status           Status        `json:"status"`
	Snapshot         *SnapshotInfo `json:"snapshot,omitempty"`
	LatestSnapshotID uuid.UUID     `json:"latestSnapshotId,omitzero"`
	Refresh          *Refresh      `json:"refresh,omitempty"`
	Issues           []Issue       `json:"issues"`
	View
}

// Rankings returns a view of the league's latest snapshot, or of snapshotID
// when set: a client paging through one snapshot passes the ID it got on
// the first page, so a refresh in between cannot mix two versions.
func (s *Service) Rankings(ctx context.Context, ref LeagueRef, snapshotID uuid.UUID, q Query) (Page, error) {
	if _, err := normalizeQuery(q); err != nil {
		return Page{}, err
	}
	ref, err := s.resolve(ctx, ref)
	if err != nil {
		return Page{}, err
	}
	latestID, refresh, err := s.latest(ctx, ref)
	if err != nil {
		return Page{}, err
	}
	target := snapshotID
	if target == uuid.Nil {
		target = latestID
	}
	if target == uuid.Nil {
		return s.emptyPage(ctx, ref, refresh, q)
	}
	snapshot, err := s.load(ctx, target)
	if err != nil {
		return Page{}, err
	}
	if snapshot.League.Season != ref.Season || snapshot.League.LeagueID != ref.LeagueID {
		return Page{}, fmt.Errorf("%w: snapshot %s belongs to league %s", ErrInvalidQuery, target, snapshot.League.LeagueKey)
	}
	view, err := snapshot.View(q)
	if err != nil {
		return Page{}, err
	}
	info := snapshot.SnapshotInfo
	page := Page{League: snapshot.League, Status: StatusReady, Snapshot: &info, LatestSnapshotID: latestID, Refresh: refresh, View: view}
	page.Issues, err = s.snapshotIssues(ctx, &info, latestID, refresh)
	if err != nil {
		return Page{}, err
	}
	page.Issues = append(page.Issues, view.Issues...)
	page.View.Issues = nil
	return page, nil
}

// Now is the service clock; converters use it to report override states.
func (s *Service) Now() time.Time {
	return s.now().UTC()
}

func (s *Service) resolve(ctx context.Context, ref LeagueRef) (LeagueRef, error) {
	if ref.LeagueKey == "" {
		if !ref.resolved() {
			return LeagueRef{}, fmt.Errorf("%w: a league key, or a season and league ID, is required", ErrUnknownLeague)
		}
		return ref, nil
	}
	season, leagueID, err := s.reader.FindLeagueKey(ctx, ref.LeagueKey)
	if err != nil {
		return LeagueRef{}, err
	}
	return LeagueRef{Season: season, LeagueID: leagueID, LeagueKey: ref.LeagueKey}, nil
}

func (s *Service) latest(ctx context.Context, ref LeagueRef) (uuid.UUID, *Refresh, error) {
	latestID, err := s.reader.LatestSnapshotID(ctx, ref.Season, ref.LeagueID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	refresh, err := s.reader.LatestRefresh(ctx, ref.Season, ref.LeagueID)
	return latestID, refresh, err
}

func (s *Service) load(ctx context.Context, id uuid.UUID) (*Snapshot, error) {
	if snapshot, cached := s.cache.get(id); cached {
		return snapshot, nil
	}
	snapshot, err := s.reader.LoadSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	s.cache.put(id, snapshot)
	return snapshot, nil
}

// emptyPage answers for a league without a snapshot: why there is none,
// from its rules and its latest refresh attempt.
func (s *Service) emptyPage(ctx context.Context, ref LeagueRef, refresh *Refresh, q Query) (Page, error) {
	page := Page{
		League: League{
			Season: ref.Season, LeagueID: ref.LeagueID, LeagueKey: ref.LeagueKey,
			Categories: []Category{}, RosterSlots: []draft.RosterSlot{},
		},
		Status: StatusNotComputed, Refresh: refresh,
		View: View{Scenario: q.Scenario, Offset: q.Offset, Limit: q.Limit, Rows: []Row{}},
	}
	rulesIssues, err := s.rulesIssues(ctx, ref, &page.League)
	if err != nil {
		return Page{}, err
	}
	page.Status, page.Issues = s.refreshStatus(refresh)
	page.Issues = append(rulesIssues, page.Issues...)
	return page, nil
}

// rulesIssues fills the league from its rules and reports missing or
// unsupported rules without computing anything.
func (s *Service) rulesIssues(ctx context.Context, ref LeagueRef, league *League) ([]Issue, error) {
	rules, err := s.reader.LeagueRules(ctx, ref.Season, ref.LeagueID)
	if err != nil {
		return nil, err
	}
	if rules == nil {
		return []Issue{{Code: IssueMissingRules, Message: fmt.Sprintf("no rules were imported for season %d league %d; run a Yahoo sync", ref.Season, ref.LeagueID)}}, nil
	}
	*league = LeagueOf(*rules)
	if _, err := draft.ScoringFor(*rules); err != nil {
		return []Issue{{Code: IssueUnsupportedScoring, Message: err.Error()}}, nil
	}
	return nil, nil
}

// refreshStatus is the status of a league without a snapshot.
func (s *Service) refreshStatus(refresh *Refresh) (Status, []Issue) {
	switch {
	case refresh == nil:
		return StatusNotComputed, []Issue{{Code: IssueNotComputed, Message: "no ranking refresh has run for this league"}}
	case refresh.State == RefreshRunning && !s.interrupted(refresh):
		return StatusRefreshing, []Issue{{Code: IssueRefreshRunning, Message: "the first ranking refresh is running"}}
	}
	return StatusFailed, []Issue{s.refreshIssue(refresh)}
}

// interrupted reports whether a running attempt outlived the refresh
// timeout: its worker stopped without recording an outcome.
func (s *Service) interrupted(refresh *Refresh) bool {
	return refresh.State == RefreshRunning && s.now().Sub(refresh.StartedAt) > RefreshTimeout
}

func (s *Service) refreshIssue(refresh *Refresh) Issue {
	switch {
	case s.interrupted(refresh):
		return Issue{Code: IssueRefreshInterrupted, Message: fmt.Sprintf("the refresh started %s never finished", refresh.StartedAt.Format(time.RFC3339))}
	case refresh.State == RefreshRunning:
		return Issue{Code: IssueRefreshRunning, Message: "a ranking refresh is running; this snapshot stays until it succeeds"}
	case refresh.State == RefreshCanceled:
		return Issue{Code: IssueRefreshCanceled, Message: "the latest ranking refresh was canceled"}
	}
	return Issue{Code: IssueRefreshFailed, Message: fmt.Sprintf("the latest ranking refresh failed (%s): %s", refresh.Code, refresh.Error)}
}

// snapshotIssues lists every condition of a served snapshot.
func (s *Service) snapshotIssues(ctx context.Context, snapshot *SnapshotInfo, latestID uuid.UUID, refresh *Refresh) ([]Issue, error) {
	issues := slices.Clone(snapshot.Unavailable)
	issues = append(issues, freshnessIssues(snapshot, s.now(), s.staleAfter)...)
	changes, err := s.reader.OverrideChanges(ctx, snapshot.League.LeagueKey, snapshot.AsOf, s.now())
	if err != nil {
		return nil, err
	}
	if changes > 0 {
		issues = append(issues, Issue{Code: IssueOverridesChanged, Message: fmt.Sprintf("%d override changes since this snapshot; refresh to apply them", changes)})
	}
	rules, err := s.reader.LeagueRules(ctx, snapshot.League.Season, snapshot.League.LeagueID)
	if err != nil {
		return nil, err
	}
	if rules != nil && rulesHash(*rules) != snapshot.League.RulesHash {
		issues = append(issues, Issue{Code: IssueRulesChanged, Message: "league rules were imported again with changes since this snapshot; refresh to rank under them"})
	}
	if latestID != uuid.Nil && latestID != snapshot.ID {
		issues = append(issues, Issue{Code: IssueNewerSnapshot, Message: fmt.Sprintf("snapshot %s is newer than this one", latestID)})
	}
	if refresh != nil && refresh.State != RefreshSucceeded && laterAttempt(refresh, snapshot) {
		issues = append(issues, s.refreshIssue(refresh))
	}
	return issues, nil
}

// laterAttempt reports whether a refresh attempt came after the snapshot.
// A snapshot's as-of time is the start of the refresh that built it, so
// both times come from the same (worker) clock.
func laterAttempt(refresh *Refresh, snapshot *SnapshotInfo) bool {
	return refresh.SnapshotID != snapshot.ID && !refresh.StartedAt.Before(snapshot.AsOf)
}
