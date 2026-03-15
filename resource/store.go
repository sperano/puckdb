package resource

import (
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/store"
)

// ReadParsed reads a resource and parses it into the typed value.
// This is a generic function that works with any Parseable resource.
func ReadParsed[T any](s store.Storage, r core.Parseable[T]) (T, error) {
	data, err := s.Read(r.Path())
	if err != nil {
		var zero T
		return zero, err
	}
	return r.Parse(data)
}

// WriteParsed serializes an object and writes it to storage.
// This is a generic function that works with any Formattable resource.
func WriteParsed[T any](s store.Storage, r core.Formattable[T], obj T) error {
	data, err := r.Format(obj)
	if err != nil {
		return err
	}
	return s.Write(r.Path(), data)
}
