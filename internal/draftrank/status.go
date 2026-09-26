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
	IssueMissingRules        IssueCode = "MISSING_RULES"
	IssueUnsupportedScoring  IssueCode = "UNSUPPORTED_SCORING"
	IssueMissingPool         IssueCode = "MISSING_POOL"
	IssueMissingProjections  IssueCode = "MISSING_PROJECTIONS"
	IssueRankingFailed       IssueCode = "RANKING_FAILED"
	IssueInternalError       IssueCode = "INTERNAL_ERROR"
	IssueRefreshCanceled     IssueCode = "REFRESH_CANCELED"
	IssueNewsAdjustmentsDown IssueCode = "NEWS_ADJUSTMENTS_UNAVAILABLE"

	// Snapshot conditions.
	IssueProvisionalRules   IssueCode = "PROVISIONAL_RULES"
	IssueNewsSourceStale    IssueCode = "NEWS_SOURCE_STALE"
	IssueNewsSourceFailing  IssueCode = "NEWS_SOURCE_FAILING"
	IssueNewsSourceMissing  IssueCode = "NEWS_SOURCE_MISSING"
	IssueStaleSnapshot      IssueCode = "STALE_SNAPSHOT"
	IssueStalePool          IssueCode = "STALE_POOL"
	IssueOverridesChanged   IssueCode = "OVERRIDES_CHANGED"
	IssueRulesChanged       IssueCode = "RULES_CHANGED"
	IssueNewerSnapshot      IssueCode = "NEWER_SNAPSHOT_AVAILABLE"
	IssueScenarioFallback   IssueCode = "SCENARIO_UNAVAILABLE"
	IssueNotComputed        IssueCode = "NOT_COMPUTED"
	IssueRefreshRunning     IssueCode = "REFRESH_RUNNING"
	IssueRefreshFailed      IssueCode = "REFRESH_FAILED"
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
