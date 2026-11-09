package shared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	failureTestActivity     = "failureTestActivity"
	failureTestMaxAttempts  = 5
	failureTestRetryDelay   = time.Second
	failureTestTransientRun = 2 // failed attempts before the transient activity succeeds
	failureTestStartToClose = time.Hour
)

func pgError(code string) error {
	return &pgconn.PgError{Code: code, Message: "test " + code}
}

func corruptFile() error {
	return fmt.Errorf("read boxscore for game 2024020001: %w",
		&resource.ParseError{Err: errors.New("unexpected end of JSON input")})
}

func batchOf(errs ...error) error {
	return fmt.Errorf("upsert skater stats: %w", &BatchError{Errors: errs, Indices: make([]int, len(errs))})
}

func requirePermanent(t *testing.T, err error, errType string) *temporal.ApplicationError {
	t.Helper()
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.True(t, appErr.NonRetryable(), "must not be retried")
	assert.Equal(t, errType, appErr.Type())
	return appErr
}

// assertUnchanged checks that got is the very error want, not a copy.
func assertUnchanged(t *testing.T, want, got error) {
	t.Helper()
	assert.True(t, want == got, "got %v, want the unchanged %v", got, want)
}

func TestClassifyActivityError_Permanent(t *testing.T) {
	t.Parallel()
	// Spelled out, not the constants, so a change to the advice fails here.
	hints := map[string]string{
		ErrTypeCorruptCachedFile: "a retry reads the same bytes; delete the cached file (for a Yahoo player pool page, the pool manifest) and re-run the fetch",
		ErrTypeRejectedValue:     "every retry sends the same value",
	}
	tests := []struct {
		name    string
		err     error
		errType string
	}{
		{"corrupt cached file", corruptFile(), ErrTypeCorruptCachedFile},
		{"string too long in a batch", batchOf(fmt.Errorf("row 3: %w", pgError(sqlStateStringTooLong))), ErrTypeRejectedValue},
		{"numeric out of range", fmt.Errorf("upsert game: %w", pgError(sqlStateNumericOutOfRange)), ErrTypeRejectedValue},
		{"invalid datetime", pgError(sqlStateInvalidDatetime), ErrTypeRejectedValue},
		{"datetime out of range", pgError(sqlStateDatetimeOutOfRange), ErrTypeRejectedValue},
		{"invalid UTF-8", pgError(sqlStateInvalidByteSequence), ErrTypeRejectedValue},
		{"untranslatable character", pgError(sqlStateUntranslatableChar), ErrTypeRejectedValue},
		{"invalid text", pgError(sqlStateInvalidText), ErrTypeRejectedValue},
		{"not null", pgError(sqlStateNotNullViolation), ErrTypeRejectedValue},
		{"check", pgError(sqlStateCheckViolation), ErrTypeRejectedValue},
		{"every joined error permanent", errors.Join(corruptFile(), pgError(sqlStateStringTooLong)), ErrTypeCorruptCachedFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyActivityError(tt.err)
			appErr := requirePermanent(t, got, tt.errType)
			assert.Equal(t, tt.err.Error()+" ("+hints[tt.errType]+")", appErr.Message(),
				"the message is the original error once, then the hint")
		})
	}
}

func TestClassifyActivityError_RetryableUnchanged(t *testing.T) {
	t.Parallel()
	retryableApp := temporal.NewApplicationError("explicitly retryable", "Custom", corruptFile())
	tests := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"missing cached file", errors.New("boxscore file missing for game 2024020001")},
		{"unreadable cached file", fmt.Errorf("read daily schedule: %w", os.ErrNotExist)},
		{"foreign key: the referenced row may come from another activity", batchOf(pgError("23503"))},
		{"unique violation: a concurrent writer", pgError("23505")},
		{"exclusion violation: a concurrent writer", pgError("23P01")},
		{"serialization failure", pgError("40001")},
		{"deadlock", pgError("40P01")},
		{"connection failure", pgError("08006")},
		{"undefined table: migration not applied yet", pgError("42P01")},
		{"query canceled", pgError("57014")},
		{"division by zero: depends on stored rows", pgError("22012")},
		{"one joined error retryable", errors.Join(corruptFile(), pgError("40001"))},
		{"joined with an unknown error", errors.Join(corruptFile(), errors.New("disk I/O"))},
		{"batch with a repairable row", batchOf(pgError(sqlStateStringTooLong), pgError("23503"))},
		{"explicit retryable ApplicationError", retryableApp},
		{"wrapped explicit ApplicationError", fmt.Errorf("import: %w", retryableApp)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertUnchanged(t, tt.err, ClassifyActivityError(tt.err))
		})
	}
}

func TestClassifyActivityError_CancellationUnchanged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
	}{
		{"canceled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
		{"canceled while a permanent error was being returned",
			fmt.Errorf("upsert game: %w", errors.Join(pgError(sqlStateStringTooLong), context.Canceled))},
		{"canceled read of a corrupt file", errors.Join(corruptFile(), context.Canceled)},
		{"temporal canceled", temporal.NewCanceledError()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyActivityError(tt.err)
			assertUnchanged(t, tt.err, got)
			var appErr *temporal.ApplicationError
			assert.False(t, errors.As(got, &appErr), "a cancellation is never an ApplicationError")
		})
	}
}

// flakyActivity fails its first failures attempts with err and counts
// every attempt.
type flakyActivity struct {
	err      error
	failures int32
	attempts atomic.Int32
}

func (a *flakyActivity) run(context.Context) error {
	if a.attempts.Add(1) <= a.failures {
		return a.err
	}
	return nil
}

func retryingWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: failureTestStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: failureTestRetryDelay, BackoffCoefficient: 1, MaximumAttempts: failureTestMaxAttempts,
		},
	})
	return workflow.ExecuteActivity(ctx, failureTestActivity).Get(ctx, nil)
}

func runRetryingWorkflow(t *testing.T, act *flakyActivity, interceptors ...interceptor.WorkerInterceptor) error {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{Interceptors: interceptors})
	env.RegisterWorkflow(retryingWorkflow)
	env.RegisterActivityWithOptions(act.run, activity.RegisterOptions{Name: failureTestActivity})
	env.ExecuteWorkflow(retryingWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	return env.GetWorkflowError()
}

func TestFailureClassifierInterceptor_PermanentFailureStopsAtFirstAttempt(t *testing.T) {
	t.Parallel()
	act := &flakyActivity{err: corruptFile(), failures: failureTestMaxAttempts}

	err := runRetryingWorkflow(t, act, &FailureClassifierInterceptor{})

	requirePermanent(t, err, ErrTypeCorruptCachedFile)
	assert.Equal(t, int32(1), act.attempts.Load())
}

func TestFailureClassifierInterceptor_WithoutItAPermanentFailureUsesEveryAttempt(t *testing.T) {
	t.Parallel()
	act := &flakyActivity{err: corruptFile(), failures: failureTestMaxAttempts}

	err := runRetryingWorkflow(t, act)

	require.Error(t, err)
	assert.Equal(t, int32(failureTestMaxAttempts), act.attempts.Load())
}

func TestFailureClassifierInterceptor_RepairableFailureIsRetried(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
	}{
		{"missing cached file", errors.New("boxscore file missing for game 2024020001")},
		{"foreign key violation", batchOf(pgError("23503"))},
		{"serialization failure", pgError("40001")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			act := &flakyActivity{err: tt.err, failures: failureTestTransientRun}

			err := runRetryingWorkflow(t, act, &FailureClassifierInterceptor{})

			require.NoError(t, err)
			assert.Equal(t, int32(failureTestTransientRun+1), act.attempts.Load())
		})
	}
}

// stubInbound is the next interceptor of failureClassifierInbound: it
// returns err as the activity's error.
type stubInbound struct {
	interceptor.ActivityInboundInterceptorBase
	err error
}

func (s *stubInbound) ExecuteActivity(context.Context, *interceptor.ExecuteActivityInput) (any, error) {
	return nil, s.err
}

func classifiedByInterceptor(ctx context.Context, err error) error {
	in := (&FailureClassifierInterceptor{}).InterceptActivity(ctx, &stubInbound{err: err})
	_, got := in.ExecuteActivity(ctx, &interceptor.ExecuteActivityInput{})
	return got
}

func TestFailureClassifierInterceptor_CanceledActivityKeepsItsError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := corruptFile()

	assertUnchanged(t, err, classifiedByInterceptor(ctx, err))
}

func TestFailureClassifierInterceptor_RunningActivityIsClassified(t *testing.T) {
	t.Parallel()
	requirePermanent(t, classifiedByInterceptor(context.Background(), corruptFile()), ErrTypeCorruptCachedFile)
}
