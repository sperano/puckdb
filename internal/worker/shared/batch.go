package shared

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	emptyBatchErrorMessage    = "batch operation failed without an error"
	batchAlreadyClosedMessage = "batch already closed"
)

// BatchCount returns the number of batches needed to process n items with the given batch size.
func BatchCount(n, batchSize int) int {
	return (n + batchSize - 1) / batchSize
}

// BatchSlice returns the sub-slice for the given batch index and size, clamping to the slice length.
func BatchSlice[T any](items []T, batchIndex, batchSize int) []T {
	start := batchIndex * batchSize
	end := start + batchSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// BatchError aggregates multiple errors from a batch operation.
// It implements error and provides Unwrap() []error for Go 1.20+ error inspection.
type BatchError struct {
	Errors  []error // All errors encountered
	Indices []int   // Row indices that failed
}

// Error returns a summary message. For a single failure, shows the full error.
// For multiple failures, shows the first error plus a count.
func (be *BatchError) Error() string {
	if be == nil || len(be.Errors) == 0 {
		return emptyBatchErrorMessage
	}
	if len(be.Errors) == 1 {
		return be.Errors[0].Error()
	}
	return fmt.Sprintf("%s (and %d more errors)", be.Errors[0].Error(), len(be.Errors)-1)
}

// Unwrap returns all errors for use with errors.Is/As on any constituent error.
func (be *BatchError) Unwrap() []error {
	if be == nil {
		return nil
	}
	return be.Errors
}

// Execer abstracts sqlc's generated *FooBatchResults types so ExecBatch is
// decoupled from any specific batch type. Every sqlc batch result satisfies it
// implicitly via its Exec(func(int, error)) method.
type Execer interface {
	Exec(func(int, error))
}

// ExecBatch runs a sqlc batch operation and returns all errors encountered.
// errContext is called with the failing row index to produce a descriptive prefix
// for the error message. The caller's closure captures params for rich context.
// Returns nil if no errors, or a *BatchError containing all failures.
func ExecBatch(br Execer, errContext func(i int) string) error {
	var batchErr *BatchError
	var primaryErr error
	br.Exec(func(i int, err error) {
		if err != nil {
			// pgx does not export a sentinel for this error. After the first
			// command fails it returns this exact message for the remaining
			// commands, which are consequences of the primary failure rather
			// than independent row failures. sqlc uses the same message when a
			// generated batch wrapper has already been closed.
			if batchErr != nil && isBatchFollowOn(primaryErr, err) {
				return
			}
			if batchErr == nil {
				batchErr = &BatchError{}
				primaryErr = err
			}
			batchErr.Errors = append(batchErr.Errors, fmt.Errorf("%s: %w", errContext(i), err))
			batchErr.Indices = append(batchErr.Indices, i)
		}
	})
	if batchErr != nil {
		return batchErr
	}
	return nil
}

// isBatchFollowOn identifies errors pgx reports for commands after the first
// failure. Depending on where the batch stopped, pgx either repeats the same
// error or reports that the batch is already closed; neither is a new row
// failure worth adding to BatchError.
func isBatchFollowOn(primary, err error) bool {
	if err.Error() == batchAlreadyClosedMessage {
		return true
	}
	var primaryPGError, currentPGError *pgconn.PgError
	return errors.As(primary, &primaryPGError) &&
		errors.As(err, &currentPGError) &&
		primaryPGError == currentPGError
}
