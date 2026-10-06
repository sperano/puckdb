package resource

import (
	"context"
	"os"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
)

// ParseError reports that a resource was read from storage but did not parse.
// It lets callers tell a corrupt cached file (recoverable by re-downloading)
// from a missing or unreadable one, while leaving the message unchanged.
type ParseError struct {
	Err error // the resource's own Parse error, returned verbatim by Error()
}

func (e *ParseError) Error() string { return e.Err.Error() }

func (e *ParseError) Unwrap() error { return e.Err }

// ReadParsed reads a resource and parses it into the typed value.
// This is a generic function that works with any Parseable resource.
// A parse failure is returned as a *ParseError.
func ReadParsed[T any](ctx context.Context, s store.Storage, r core.Parseable[T]) (T, error) {
	var zero T
	data, err := Read(ctx, s, r)
	if err != nil {
		return zero, err
	}
	obj, err := r.Parse(data)
	if err != nil {
		return zero, &ParseError{Err: err}
	}
	return obj, nil
}

// WriteParsed serializes an object and writes it to storage.
// This is a generic function that works with any Formattable resource.
func WriteParsed[T any](ctx context.Context, s store.Storage, r core.Formattable[T], obj T) error {
	data, err := r.Format(obj)
	if err != nil {
		return err
	}
	return Write(ctx, s, r, data)
}

// The helpers below are the storage boundary for resources: each one runs
// the operation on r's path with the storage labeled by r's file type, so
// filesystem metrics carry r.Type() instead of guessing it from the path.

// Read returns the raw contents of r's file.
func Read(ctx context.Context, s store.Storage, r core.Resource) ([]byte, error) {
	return store.WithFileType(s, r.Type()).Read(ctx, r.Path())
}

// Write stores data as r's file.
func Write(ctx context.Context, s store.Storage, r core.Resource, data []byte) error {
	return store.WithFileType(s, r.Type()).Write(ctx, r.Path(), data)
}

// Exists reports whether r's file is stored.
func Exists(ctx context.Context, s store.Storage, r core.Resource) bool {
	return store.WithFileType(s, r.Type()).Exists(ctx, r.Path())
}

// Delete removes r's file; a missing file is not an error.
func Delete(ctx context.Context, s store.Storage, r core.Resource) error {
	return store.WithFileType(s, r.Type()).Delete(ctx, r.Path())
}

// Stat returns the file information of r's file.
func Stat(ctx context.Context, s store.Storage, r core.Resource) (os.FileInfo, error) {
	return store.WithFileType(s, r.Type()).Stat(ctx, r.Path())
}
