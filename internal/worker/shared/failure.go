package shared

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sperano/puckdb/internal/resource"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
)

// ApplicationError types of the failures ClassifyActivityError marks
// non-retryable.
const (
	// ErrTypeCorruptCachedFile is a cached file that was read but does not
	// parse. Cache writes are atomic, so the bytes a retry reads are the
	// same; only a new download (re-running the fetch) replaces them.
	ErrTypeCorruptCachedFile = "CorruptCachedFile"
	// ErrTypeRejectedValue is a value PostgreSQL refuses for its column. The
	// activity sends the same values again on every attempt.
	ErrTypeRejectedValue = "RejectedValue"
)

// Hints appended to the message of a permanent failure.
const (
	// Fetches skip a file already cached, and a Yahoo player pool page is
	// downloaded again only with a new pool, which a missing manifest starts.
	corruptCachedFileHint = "a retry reads the same bytes; delete the cached file (for a Yahoo player pool page, the pool manifest) and re-run the fetch"
	rejectedValueHint     = "every retry sends the same value"
)

// SQLSTATE codes of value errors: PostgreSQL rejects a statement's own
// values, whatever the other rows hold. Integrity errors that depend on
// other rows stay retryable: a foreign key violation (23503) is repaired by
// the activity that writes the referenced row, and a unique (23505) or
// exclusion (23P01) violation is lost to a concurrent writer.
const (
	sqlStateStringTooLong       = "22001"
	sqlStateNumericOutOfRange   = "22003"
	sqlStateInvalidDatetime     = "22007"
	sqlStateDatetimeOutOfRange  = "22008"
	sqlStateInvalidByteSequence = "22021"
	sqlStateInvalidText         = "22P02"
	sqlStateUntranslatableChar  = "22P05"
	sqlStateNotNullViolation    = "23502"
	sqlStateCheckViolation      = "23514"
)

var rejectedValueSQLStates = map[string]bool{
	sqlStateStringTooLong:       true,
	sqlStateNumericOutOfRange:   true,
	sqlStateInvalidDatetime:     true,
	sqlStateDatetimeOutOfRange:  true,
	sqlStateInvalidByteSequence: true,
	sqlStateInvalidText:         true,
	sqlStateUntranslatableChar:  true,
	sqlStateNotNullViolation:    true,
	sqlStateCheckViolation:      true,
}

// ClassifyActivityError returns err as a non-retryable ApplicationError when
// no retry of the activity can succeed: a cached file that does not parse
// (*resource.ParseError) or a value PostgreSQL rejects (rejectedValueSQLStates).
// Everything else is returned unchanged and keeps the retry policy:
// cancellations and deadlines, errors already carrying a Temporal error,
// missing cached files (a fetch running next to the import writes them, the
// schedule before its games), other database errors (connections,
// serialization, foreign keys, a migration not applied yet) and unknown
// errors. An error joining several (errors.Join, *BatchError) is permanent
// only when each of them is.
func ClassifyActivityError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var appErr *temporal.ApplicationError
	var canceledErr *temporal.CanceledError
	if errors.As(err, &appErr) || errors.As(err, &canceledErr) {
		return err
	}
	switch permanentErrorType(err) {
	case ErrTypeCorruptCachedFile:
		return permanentFailure(err, ErrTypeCorruptCachedFile, corruptCachedFileHint)
	case ErrTypeRejectedValue:
		return permanentFailure(err, ErrTypeRejectedValue, rejectedValueHint)
	default:
		return err
	}
}

// permanentFailure is err as a non-retryable ApplicationError whose message
// (what workflows and the Temporal UI show) is err's own followed by hint.
func permanentFailure(err error, errType, hint string) error {
	return temporal.NewNonRetryableApplicationError(fmt.Sprintf("%v (%s)", err, hint), errType, nil)
}

// permanentErrorType returns the ApplicationError type of a permanent
// failure, or "" when err may still succeed on a retry.
func permanentErrorType(err error) string {
	switch e := err.(type) {
	case *resource.ParseError:
		return ErrTypeCorruptCachedFile
	case *pgconn.PgError:
		if rejectedValueSQLStates[e.Code] {
			return ErrTypeRejectedValue
		}
		return ""
	case interface{ Unwrap() []error }:
		return allPermanentErrorType(e.Unwrap())
	}
	if next := errors.Unwrap(err); next != nil {
		return permanentErrorType(next)
	}
	return ""
}

// allPermanentErrorType returns the first error's permanent type when every
// error of errs is permanent, or "" otherwise.
func allPermanentErrorType(errs []error) string {
	first := ""
	for i, err := range errs {
		errType := permanentErrorType(err)
		if errType == "" {
			return ""
		}
		if i == 0 {
			first = errType
		}
	}
	return first
}

// FailureClassifierInterceptor applies ClassifyActivityError to the error of
// every activity the worker runs, so a failure no retry can fix ends the
// activity at once instead of after the whole retry policy. An activity whose
// context is done keeps its error as is.
type FailureClassifierInterceptor struct {
	interceptor.WorkerInterceptorBase
}

// InterceptActivity implements interceptor.WorkerInterceptor.
func (*FailureClassifierInterceptor) InterceptActivity(
	_ context.Context, next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	in := &failureClassifierInbound{}
	in.Next = next
	return in
}

type failureClassifierInbound struct {
	interceptor.ActivityInboundInterceptorBase
}

func (i *failureClassifierInbound) ExecuteActivity(ctx context.Context, in *interceptor.ExecuteActivityInput) (any, error) {
	result, err := i.Next.ExecuteActivity(ctx, in)
	if err == nil || ctx.Err() != nil {
		return result, err
	}
	return result, ClassifyActivityError(err)
}
