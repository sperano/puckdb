package draftrank

import (
	"time"

	"github.com/google/uuid"
)

// IssueCode names a condition a ranking consumer must be told about. Every
// missing input, unsupported rule, stale source and failed refresh has one,
// so clients can react to it without parsing messages.
type IssueCode string

const (
	// Refresh failures (also the State of a failed refresh).

	// IssueMissingRules: no league rules were imported (run a Yahoo sync).
	IssueMissingRules IssueCode = "MISSING_RULES"
	// IssueUnsupportedScoring: the league's scoring cannot be ranked.
	IssueUnsupportedScoring IssueCode = "UNSUPPORTED_SCORING"
	// IssueMissingPool: the league has no draftable player pool.
	IssueMissingPool IssueCode = "MISSING_POOL"
	// IssueMissingProjections: the baseline projection failed coverage
	// validation (projection.CoverageError): an enabled scoring category the
	// model cannot project or no draftable player of its kind, a draftable
	// player whose projection lacks a category, or no draftable pool.
	IssueMissingProjections IssueCode = "MISSING_PROJECTIONS"
	// IssueRankingFailed: the baseline or a scenario ranking failed.
	IssueRankingFailed IssueCode = "RANKING_FAILED"
	// IssueInternalError: a refresh failure with no more specific code.
	IssueInternalError IssueCode = "INTERNAL_ERROR"
	// IssueRefreshCanceled: the refresh was canceled before it finished.
	IssueRefreshCanceled IssueCode = "REFRESH_CANCELED"
	// IssueNewsAdjustmentsDown: news adjustments could not be applied, so
	// the snapshot holds only the baseline ranking. A snapshot condition
	// too: the refresh still succeeds.
	IssueNewsAdjustmentsDown IssueCode = "NEWS_ADJUSTMENTS_UNAVAILABLE"

	// Snapshot conditions.

	// IssueProvisionalRules: the rules come from a temporary stand-in league.
	IssueProvisionalRules IssueCode = "PROVISIONAL_RULES"
	// IssueNewsSourceStale: a news source's newest data was stale when the
	// snapshot was built, so recent news may be missing from the scenarios.
	IssueNewsSourceStale IssueCode = "NEWS_SOURCE_STALE"
	// IssueNewsSourceFailing: a news source was failing when the snapshot
	// was built.
	IssueNewsSourceFailing IssueCode = "NEWS_SOURCE_FAILING"
	// IssueNewsSourceMissing: a news source had never been fetched
	// successfully when the snapshot was built, so its news was unavailable.
	IssueNewsSourceMissing IssueCode = "NEWS_SOURCE_MISSING"
	// IssueStaleSnapshot: the snapshot is older than the service's
	// StaleAfter.
	IssueStaleSnapshot IssueCode = "STALE_SNAPSHOT"
	// IssueStalePool: the player pool the snapshot ranked was fetched longer
	// ago than StaleAfter.
	IssueStalePool IssueCode = "STALE_POOL"
	// IssueOverridesChanged: manager overrides changed since the snapshot.
	IssueOverridesChanged IssueCode = "OVERRIDES_CHANGED"
	// IssueRulesChanged: the league rules were re-imported with changes
	// since the snapshot.
	IssueRulesChanged IssueCode = "RULES_CHANGED"
	// IssueRosterSlotsChanged: the rules change touches the roster slots,
	// so the snapshot's slot allocation no longer describes the league and
	// advice is withheld (AdviceWithheld).
	IssueRosterSlotsChanged IssueCode = "ROSTER_SLOTS_CHANGED"
	// IssueScoringChanged: the rules change touches the scoring, so the
	// snapshot's values no longer describe the league and advice is withheld.
	IssueScoringChanged IssueCode = "SCORING_CHANGED"
	// IssueNewerSnapshot: a newer snapshot of the league exists than the
	// one requested.
	IssueNewerSnapshot IssueCode = "NEWER_SNAPSHOT_AVAILABLE"
	// IssueScenarioFallback: the requested scenario is missing, so the
	// baseline ranking is served instead.
	IssueScenarioFallback IssueCode = "SCENARIO_UNAVAILABLE"
	// IssueScenarioMissing: neither the requested scenario nor the baseline
	// is in the snapshot.
	IssueScenarioMissing IssueCode = "SCENARIO_MISSING"

	// Refresh status, with or without a snapshot.

	// IssueNotComputed: no refresh has run for the league.
	IssueNotComputed IssueCode = "NOT_COMPUTED"
	// IssueRefreshRunning: a refresh is running.
	IssueRefreshRunning IssueCode = "REFRESH_RUNNING"
	// IssueRefreshFailed: the latest refresh failed; its own code is in the
	// message.
	IssueRefreshFailed IssueCode = "REFRESH_FAILED"
	// IssueRefreshInterrupted: a refresh started more than RefreshTimeout
	// ago and never finished.
	IssueRefreshInterrupted IssueCode = "REFRESH_INTERRUPTED"
)

// Issue is one condition with a human-readable message.
type Issue struct {
	Code    IssueCode `json:"code"`
	Message string    `json:"message"`
}

// Status is whether a league has a ranking to serve.
type Status string

const (
	// StatusReady means a snapshot is served (possibly with issues).
	StatusReady Status = "READY"
	// StatusNotComputed means no refresh ever ran for the league.
	StatusNotComputed Status = "NOT_COMPUTED"
	// StatusRefreshing means the first refresh is still running.
	StatusRefreshing Status = "REFRESHING"
	// StatusFailed means no snapshot exists and the last refresh failed.
	StatusFailed Status = "FAILED"
)

// RefreshState is the state of one refresh attempt.
type RefreshState string

// Refresh attempt states; a running refresh older than RefreshTimeout is
// reported as interrupted.
const (
	RefreshRunning   RefreshState = "running"
	RefreshSucceeded RefreshState = "succeeded"
	RefreshFailed    RefreshState = "failed"
	RefreshCanceled  RefreshState = "canceled"
)

// Refresh is a league's latest refresh attempt. Code is set when it failed.
type Refresh struct {
	ID         uuid.UUID    `json:"id"`
	RunID      string       `json:"runId"`
	State      RefreshState `json:"state"`
	Code       IssueCode    `json:"code,omitempty"`
	Error      string       `json:"error,omitempty"`
	SnapshotID uuid.UUID    `json:"snapshotId,omitzero"`
	StartedAt  time.Time    `json:"startedAt"`
	FinishedAt time.Time    `json:"finishedAt,omitzero"`
}

// RefreshError is a refresh failure with its issue code.
type RefreshError struct {
	Code IssueCode
	Err  error
}

func (e *RefreshError) Error() string { return string(e.Code) + ": " + e.Err.Error() }

func (e *RefreshError) Unwrap() error { return e.Err }

func refreshError(code IssueCode, err error) error {
	return &RefreshError{Code: code, Err: err}
}
