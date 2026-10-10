package draftboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
)

var (
	// ErrVersionConflict means the caller's expected board version is no
	// longer current. The error also wraps a *draftwatch.StaleStateVersionError
	// carrying the expected and current versions.
	ErrVersionConflict = errors.New("draft board state version conflict")
	ErrNoOwnedTeam     = errors.New("owned Yahoo draft team is unavailable")
)

// LeagueData contains current imported rules and the user's imported roster.
type LeagueData struct {
	Rules          draft.Snapshot
	TeamID         int
	TeamName       string
	DraftPosition  int
	Roster         []draft.RosterPlayer
	DraftFormat    string
	PickClock      *int
	EstimatedOrder []draftrecommend.PickSlot
}

// DataSource isolates persistence so board composition can be tested without
// PostgreSQL. Shortlist mutation must compare against the persisted session.
type DataSource interface {
	ResolveIdentity(context.Context, string, int) (draftwatch.Identity, error)
	Session(context.Context, draftwatch.Identity) (draftwatch.Session, error)
	Ranking(context.Context, draftwatch.Identity) (*draftrank.Snapshot, error)
	LeagueData(context.Context, draftwatch.Identity) (LeagueData, error)
	ListShortlist(context.Context, string) ([]string, error)
	SetShortlist(context.Context, string, string, uint64, bool) error
	ApplyManual(context.Context, draftwatch.Identity, draftsession.ManualOperation, uint64, time.Time) (draftwatch.Session, draftsession.Report, error)
	ResolveConflict(context.Context, draftwatch.Identity, draftsession.PickKey, draftsession.ConflictChoice, uint64, time.Time) (draftwatch.Session, draftsession.Report, error)
	ListEvents(context.Context, string, uint64, int) ([]draftwatch.Event, error)
}

// RecommendationEngine evaluates and persists the frozen board input.
type RecommendationEngine interface {
	Recommend(context.Context, draftrecommend.Input) (draftrecommend.StoredRun, error)
}

// Refresher performs the shared draftwatch one-shot poll.
type Refresher interface {
	SyncOnce(context.Context, draftwatch.Identity) (draftwatch.Session, draftsession.Report, error)
}

// Options control stale presentation and the service clock.
type Options struct {
	StaleAfter time.Duration
	Now        func() time.Time
	Watch      *WatchController
}

// Service is the application boundary for a composed live draft board.
type Service struct {
	data                DataSource
	recommendations     RecommendationEngine
	refresher           Refresher
	watch               *WatchController
	staleAfter          time.Duration
	now                 func() time.Time
	recommendationMu    sync.Mutex
	recommendationCache map[string]draftrecommend.StoredRun
}

func NewService(data DataSource, recommendations RecommendationEngine, refresher Refresher, options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{data: data, recommendations: recommendations, refresher: refresher,
		watch: options.Watch, staleAfter: options.StaleAfter, now: options.Now,
		recommendationCache: make(map[string]draftrecommend.StoredRun)}
}

// ResolveIdentity returns the season-safe identity used to validate Yahoo
// team and player keys in manual controls.
func (s *Service) ResolveIdentity(ctx context.Context, league string, season int) (draftwatch.Identity, error) {
	if s == nil || s.data == nil {
		return draftwatch.Identity{}, errors.New("draft board data source is required")
	}
	return s.data.ResolveIdentity(ctx, league, season)
}

// Status derives the same freshness state used by full board responses.
func (s *Service) Status(session draftwatch.Session) Status {
	return statusOf(session, s.now().UTC(), s.staleAfter)
}

// Board loads current session/rules/ranking state and saves its deterministic
// recommendation before returning it.
func (s *Service) Board(ctx context.Context, request Request) (Board, error) {
	if s.data == nil {
		return Board{}, errors.New("draft board data source is required")
	}
	inputs, err := s.loadBoardInputs(ctx, request)
	if err != nil {
		return Board{}, err
	}
	now := s.now().UTC()
	status := statusOf(inputs.session, now, s.staleAfter)
	roster := rosterOf(inputs.session.State, inputs.league.Roster, inputs.ranking,
		inputs.league.Rules.Rules.RosterSlots, inputs.league.TeamID)
	recommendation, result, err := s.boardRecommendation(ctx, request, inputs, roster, status, now)
	if err != nil {
		return Board{}, err
	}
	board := newBoard(inputs, roster, status, recommendation, result, now)
	return s.finishBoard(ctx, board, inputs.ranking, roster, result, recommendation)
}

type boardInputs struct {
	identity draftwatch.Identity
	session  draftwatch.Session
	ranking  *draftrank.Snapshot
	league   LeagueData
}

func (s *Service) loadBoardInputs(ctx context.Context, request Request) (boardInputs, error) {
	var inputs boardInputs
	var err error
	inputs.identity, err = s.data.ResolveIdentity(ctx, request.League, request.Season)
	if err != nil {
		return inputs, err
	}
	inputs.session, err = s.data.Session(ctx, inputs.identity)
	if err == nil {
		inputs.ranking, err = s.data.Ranking(ctx, inputs.identity)
	}
	if err == nil {
		inputs.league, err = s.data.LeagueData(ctx, inputs.identity)
	}
	if err != nil {
		return inputs, err
	}
	if inputs.league.TeamID <= 0 {
		return inputs, ErrNoOwnedTeam
	}
	if inputs.ranking.League.LeagueKey != inputs.identity.LeagueKey {
		return inputs, fmt.Errorf("ranking snapshot league %s differs from session %s",
			inputs.ranking.League.LeagueKey, inputs.identity.LeagueKey)
	}
	return inputs, nil
}

func (s *Service) boardRecommendation(ctx context.Context, request Request, inputs boardInputs, roster Roster,
	status Status, now time.Time) (*draftrecommend.StoredRun, draftrecommend.Result, error) {
	input := draftrecommend.Input{
		Session: inputs.session.State, SessionLeagueKey: inputs.identity.LeagueKey,
		SessionSafe: inputs.session.RecommendationsSafe, SessionStale: status.Stale,
		Ranking: inputs.ranking, Scenario: request.Scenario, OurTeamID: inputs.league.TeamID,
		Roster: roster.Players, Order: slices.Clone(request.Order), Strategy: request.Strategy,
		Availability: request.Availability, ADP: request.ADP,
		RankingIssues: slices.Clone(inputs.ranking.Unavailable), GeneratedAt: now,
	}
	result := draftrecommend.Result{Scenario: selectedScenario(inputs.ranking, request.Scenario)}
	if s.recommendations == nil {
		return nil, result, nil
	}
	run, err := s.recommend(ctx, input)
	if err != nil && !errors.Is(err, draftrecommend.ErrUnsafeRoster) {
		return nil, result, err
	}
	if err != nil {
		return nil, result, nil
	}
	return &run, run.Result, nil
}

func newBoard(inputs boardInputs, roster Roster, status Status, recommendation *draftrecommend.StoredRun,
	result draftrecommend.Result, now time.Time) Board {
	board := Board{
		Identity: inputs.identity, League: inputs.ranking.League, Snapshot: inputs.ranking.SnapshotInfo,
		Scenario: result.Scenario, DraftFormat: inputs.league.DraftFormat,
		PickClockSecs: cloneInt(inputs.league.PickClock), TeamID: inputs.league.TeamID,
		TeamName: inputs.league.TeamName, DraftPosition: inputs.league.DraftPosition,
		Session: status, Picks: picksOf(inputs.session.State, inputs.ranking), Roster: roster,
		Available:      availablePlayers(inputs.ranking, result, inputs.session.State, roster.Players),
		Recommendation: recommendation, CurrentPick: clonePickSlot(result.CurrentPick),
		PicksUntilTurn: cloneInt(result.PicksUntilNextTurn), GeneratedAt: now,
	}
	if board.CurrentPick == nil && status.RecommendationsSafe && !status.Stale {
		board.CurrentPick, board.PicksUntilTurn = estimatedTurn(inputs.session.State,
			inputs.league.EstimatedOrder, inputs.league.TeamID)
		board.TurnEstimated = board.CurrentPick != nil
	}
	return board
}

func (s *Service) finishBoard(ctx context.Context, board Board, ranking *draftrank.Snapshot, roster Roster,
	result draftrecommend.Result, recommendation *draftrecommend.StoredRun) (Board, error) {
	var err error
	board.Shortlist, err = s.data.ListShortlist(ctx, board.Identity.LeagueKey)
	if err != nil {
		return Board{}, err
	}
	shortlisted := make(map[string]bool, len(board.Shortlist))
	for _, playerKey := range board.Shortlist {
		shortlisted[playerKey] = true
	}
	for index := range board.Available {
		board.Available[index].IsShortlisted = shortlisted[board.Available[index].PlayerKey]
	}
	warnings := board.Session.Warnings
	for _, warning := range roster.Warnings {
		warnings = appendIssues(warnings, draftrank.Issue{Code: IssueRosterWarning, Message: warning})
	}
	warnings = appendIssues(warnings, ranking.Unavailable...)
	warnings = appendIssues(warnings, result.Issues...)
	if s.recommendations == nil {
		warnings = appendIssues(warnings, issueRecommendationsUnavailable)
	}
	if recommendation == nil && s.recommendations != nil {
		warnings = appendIssues(warnings, issueRecommendationsSuppressed)
	}
	board.Session.Warnings = warnings
	return board, nil
}

func (s *Service) recommend(ctx context.Context, input draftrecommend.Input) (draftrecommend.StoredRun, error) {
	key, err := recommendationCacheKey(input)
	if err != nil {
		return draftrecommend.StoredRun{}, err
	}
	s.recommendationMu.Lock()
	defer s.recommendationMu.Unlock()
	if cached, exists := s.recommendationCache[key]; exists {
		return cached, nil
	}
	run, err := s.recommendations.Recommend(ctx, input)
	if err == nil {
		s.recommendationCache[key] = run
	}
	return run, err
}

func recommendationCacheKey(input draftrecommend.Input) (string, error) {
	boundary := struct {
		League       string
		Version      uint64
		Ranking      string
		Scenario     draftrank.Scenario
		TeamID       int
		Roster       []draft.RosterPlayer
		Order        []draftrecommend.PickSlot
		Strategy     draftrecommend.Strategy
		Availability draftrecommend.AvailabilitySource
		ADP          draftrecommend.ADPSource
		SessionSafe  bool
		SessionStale bool
	}{
		League: input.SessionLeagueKey, Version: input.Session.Version,
		Ranking: input.Ranking.Identity, Scenario: selectedScenario(input.Ranking, input.Scenario),
		TeamID: input.OurTeamID, Roster: input.Roster, Order: input.Order,
		Strategy: input.Strategy, Availability: input.Availability, ADP: input.ADP,
		SessionSafe: input.SessionSafe, SessionStale: input.SessionStale,
	}
	raw, err := json.Marshal(boundary)
	if err != nil {
		return "", fmt.Errorf("encode draft recommendation cache key: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func selectedScenario(ranking *draftrank.Snapshot, requested draftrank.Scenario) draftrank.Scenario {
	if requested == "" {
		requested = draftrank.DefaultScenario
	}
	if ranking.HasScenario(requested) {
		return requested
	}
	return draftrank.ScenarioBaseline
}

// Refresh polls Yahoo through the shared draftwatch runner.
func (s *Service) Refresh(ctx context.Context, league string, season int) (draftwatch.Session, draftsession.Report, error) {
	if s.refresher == nil {
		return draftwatch.Session{}, draftsession.Report{}, errors.New("draft refresh runner is unavailable")
	}
	identity, err := s.data.ResolveIdentity(ctx, league, season)
	if err != nil {
		return draftwatch.Session{}, draftsession.Report{}, err
	}
	return s.refresher.SyncOnce(ctx, identity)
}

// StartWatch starts one process-scoped watcher for a league.
func (s *Service) StartWatch(ctx context.Context, league string, season int) (WatchStatus, error) {
	if s.watch == nil {
		return WatchStatus{}, errors.New("draft watch controller is unavailable")
	}
	return s.watch.Start(ctx, league, season)
}

// StopWatch cancels the process-scoped watcher and waits for its final poll.
func (s *Service) StopWatch(ctx context.Context, league string, season int) (WatchStatus, error) {
	if s.watch == nil {
		return WatchStatus{}, errors.New("draft watch controller is unavailable")
	}
	identity, err := s.data.ResolveIdentity(ctx, league, season)
	if err != nil {
		return WatchStatus{}, err
	}
	return s.watch.Stop(ctx, identity.LeagueKey)
}

// WatchStatus returns current or last process-local watch status.
func (s *Service) WatchStatus(leagueKey string) (WatchStatus, bool) {
	if s.watch == nil {
		return WatchStatus{}, false
	}
	return s.watch.Status(leagueKey)
}

// Close stops process-scoped watchers. Persisted sessions remain available.
func (s *Service) Close(ctx context.Context) error {
	if s == nil || s.watch == nil {
		return nil
	}
	return s.watch.Close(ctx)
}

// SetShortlist changes one player only when the caller's board version is
// still current. The store repeats this check while locking the session row.
func (s *Service) SetShortlist(ctx context.Context, league string, season int, playerKey string, expectedVersion uint64, selected bool) error {
	identity, err := s.data.ResolveIdentity(ctx, league, season)
	if err != nil {
		return err
	}
	session, err := s.data.Session(ctx, identity)
	if err != nil {
		return err
	}
	if session.State.Version != expectedVersion {
		return versionConflict(&draftwatch.StaleStateVersionError{Expected: expectedVersion, Actual: session.State.Version})
	}
	if playerKey == "" {
		return errors.New("shortlist player key is required")
	}
	return s.data.SetShortlist(ctx, identity.LeagueKey, playerKey, expectedVersion, selected)
}

// ApplyManual records an add, correction or undo against an expected reducer version.
func (s *Service) ApplyManual(ctx context.Context, league string, season int, operation draftsession.ManualOperation,
	expectedVersion uint64, at time.Time) (MutationResult, error) {
	identity, err := s.data.ResolveIdentity(ctx, league, season)
	if err != nil {
		return MutationResult{}, err
	}
	session, report, err := s.data.ApplyManual(ctx, identity, operation, expectedVersion, at)
	if errors.Is(err, draftwatch.ErrStaleStateVersion) {
		return MutationResult{}, versionConflict(err)
	}
	return MutationResult{Session: session, Report: report}, err
}

// ResolveConflict applies one explicit manual/upstream conflict choice.
func (s *Service) ResolveConflict(ctx context.Context, league string, season int, key draftsession.PickKey,
	choice draftsession.ConflictChoice, expectedVersion uint64, at time.Time) (MutationResult, error) {
	identity, err := s.data.ResolveIdentity(ctx, league, season)
	if err != nil {
		return MutationResult{}, err
	}
	session, report, err := s.data.ResolveConflict(ctx, identity, key, choice, expectedVersion, at)
	if errors.Is(err, draftwatch.ErrStaleStateVersion) {
		return MutationResult{}, versionConflict(err)
	}
	return MutationResult{Session: session, Report: report}, err
}

// Events returns ordered reducer events newer than the client's state cursor.
func (s *Service) Events(ctx context.Context, league string, season int, afterVersion uint64, limit int) ([]Event, error) {
	identity, err := s.data.ResolveIdentity(ctx, league, season)
	if err != nil {
		return nil, err
	}
	events, err := s.data.ListEvents(ctx, identity.LeagueKey, afterVersion, limit)
	if err != nil {
		return nil, err
	}
	result := make([]Event, len(events))
	for index, event := range events {
		result[index] = Event{StateVersion: event.StateVersion, Kind: event.Kind,
			Details: append([]byte(nil), event.Details...), CreatedAt: event.CreatedAt}
	}
	return result, nil
}
