package resource

import (
	"context"

	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/store"
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
	data, err := s.Read(ctx, r.Path())
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
	return s.Write(ctx, r.Path(), data)
}
