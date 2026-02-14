package store

import (
	"os"
	"reflect"
	"time"

	"github.com/sperano/puckdb/metrics"
)

// InstrumentedStore wraps a Store to record metrics for all operations
type InstrumentedStore struct {
	inner Store
}

// NewInstrumentedStore creates an instrumented wrapper around the given Store
func NewInstrumentedStore(inner Store) *InstrumentedStore {
	return &InstrumentedStore{inner: inner}
}

// fileTypeName extracts a label-friendly name from the File's concrete type
func fileTypeName(file File) string {
	return reflect.TypeOf(file).Name()
}

// Read reads a file and records timing/size metrics
func (fs *InstrumentedStore) Read(file File) ([]byte, error) {
	start := time.Now()
	data, err := fs.inner.Read(file)
	duration := time.Since(start)

	ft := fileTypeName(file)
	if err == nil {
		metrics.ObserveFSOp("read", ft, duration, len(data))
	} else {
		metrics.ObserveFSOp("read", ft, duration, 0)
	}
	return data, err
}

// Write writes a file and records timing/size metrics
func (fs *InstrumentedStore) Write(file File, content []byte) error {
	start := time.Now()
	err := fs.inner.Write(file, content)
	duration := time.Since(start)

	ft := fileTypeName(file)
	if err == nil {
		metrics.ObserveFSOp("write", ft, duration, len(content))
	} else {
		metrics.ObserveFSOp("write", ft, duration, 0)
	}
	return err
}

// Exists checks if a file exists and records timing metrics
func (fs *InstrumentedStore) Exists(file File) bool {
	start := time.Now()
	exists := fs.inner.Exists(file)
	duration := time.Since(start)

	metrics.ObserveFSOp("exists", fileTypeName(file), duration, 0)
	return exists
}

// MkdirAll creates directories and records timing metrics
func (fs *InstrumentedStore) MkdirAll(dir string, perm os.FileMode) error {
	start := time.Now()
	err := fs.inner.MkdirAll(dir, perm)
	duration := time.Since(start)

	// Use "directory" as file type since we only have a path string
	metrics.ObserveFSOp("mkdir", "directory", duration, 0)
	return err
}

// Remove deletes a file and records timing metrics
func (fs *InstrumentedStore) Remove(file File) error {
	start := time.Now()
	err := fs.inner.Remove(file)
	duration := time.Since(start)

	metrics.ObserveFSOp("remove", fileTypeName(file), duration, 0)
	return err
}

// FullPath delegates to the inner Store (no timing needed)
func (fs *InstrumentedStore) FullPath(file File) string {
	return fs.inner.FullPath(file)
}

// Inner returns the underlying FileStore for operations that need direct access.
func (fs *InstrumentedStore) Inner() *FileStore {
	return fs.inner.(*FileStore)
}
