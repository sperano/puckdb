package store

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
)

// Filesystem operation names used as the "operation" metric label.
const (
	opRead   = "read"
	opWrite  = "write"
	opExists = "exists"
	opDelete = "delete"
	opList   = "list"
	opStat   = "stat"
)

// FileTypeLabeler is implemented by Storage wrappers whose operations carry a
// file type, such as InstrumentedStorage labeling its metrics. Wrappers that
// delegate to another Storage should implement it too, so the label reaches
// the instrumented layer underneath.
type FileTypeLabeler interface {
	// WithFileType returns a view of the storage whose operations are
	// labeled with ft.
	WithFileType(ft core.FileType) Storage
}

// WithFileType returns s with its operations labeled as ft. A Storage that
// does not implement FileTypeLabeler is returned unchanged.
//
// Callers holding a core.Resource should use the resource package helpers
// (resource.Read, resource.Exists, ...), which pass the resource's own type.
func WithFileType(s Storage, ft core.FileType) Storage {
	if l, ok := s.(FileTypeLabeler); ok {
		return l.WithFileType(ft)
	}
	return s
}

// fsObserver records one filesystem operation.
type fsObserver func(operation string, ft core.FileType, duration time.Duration, bytes int)

// InstrumentedStorage wraps a Storage to record metrics for all operations.
// The file_type label comes from WithFileType; an operation issued without
// one is labeled core.Unknown, and the first such operation of each kind is
// logged as a warning so a caller that forgot to pass its type is noticed.
type InstrumentedStorage struct {
	inner     Storage
	fileType  core.FileType
	observe   fsObserver
	unlabeled *unlabeledWarner
}

// NewInstrumentedStorage creates an instrumented wrapper around the given Storage.
func NewInstrumentedStorage(inner Storage) *InstrumentedStorage {
	return &InstrumentedStorage{
		inner:     inner,
		observe:   metrics.ObserveFSOp,
		unlabeled: &unlabeledWarner{logger: log.Logger},
	}
}

// WithFileType returns a view of s whose metrics are labeled with ft. The
// view shares s's inner storage, observer and unlabeled-operation warnings.
func (s *InstrumentedStorage) WithFileType(ft core.FileType) Storage {
	labeled := *s
	labeled.inner = WithFileType(s.inner, ft)
	labeled.fileType = ft
	return &labeled
}

// record observes one operation under the storage's file type, warning once
// per operation kind when that type is unknown.
func (s *InstrumentedStorage) record(operation, path string, start time.Time, bytes int) {
	duration := time.Since(start)
	if s.fileType == core.Unknown {
		s.unlabeled.warn(operation, path)
	}
	s.observe(operation, s.fileType, duration, bytes)
}

// Read reads from storage and records timing/size metrics.
func (s *InstrumentedStorage) Read(ctx context.Context, path string) ([]byte, error) {
	start := time.Now()
	data, err := s.inner.Read(ctx, path)
	s.record(opRead, path, start, successBytes(len(data), err))
	return data, err
}

// Write writes to storage and records timing/size metrics.
func (s *InstrumentedStorage) Write(ctx context.Context, path string, data []byte) error {
	start := time.Now()
	err := s.inner.Write(ctx, path, data)
	s.record(opWrite, path, start, successBytes(len(data), err))
	return err
}

// Exists checks if a path exists and records timing metrics.
func (s *InstrumentedStorage) Exists(ctx context.Context, path string) bool {
	start := time.Now()
	exists := s.inner.Exists(ctx, path)
	s.record(opExists, path, start, 0)
	return exists
}

// Delete removes a path and records timing metrics.
func (s *InstrumentedStorage) Delete(ctx context.Context, path string) error {
	start := time.Now()
	err := s.inner.Delete(ctx, path)
	s.record(opDelete, path, start, 0)
	return err
}

// List lists files and records timing metrics.
func (s *InstrumentedStorage) List(ctx context.Context, dir string, ext string) ([]string, error) {
	start := time.Now()
	files, err := s.inner.List(ctx, dir, ext)
	s.record(opList, dir, start, len(files))
	return files, err
}

// Stat returns file information and records timing metrics.
func (s *InstrumentedStorage) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	start := time.Now()
	info, err := s.inner.Stat(ctx, path)
	s.record(opStat, path, start, 0)
	return info, err
}

// successBytes returns n when the operation succeeded and 0 otherwise, so a
// failed read or write records no size.
func successBytes(n int, err error) int {
	if err != nil {
		return 0
	}
	return n
}

// unlabeledWarner logs the first operation of each kind that reaches the
// instrumented storage without a file type. Later ones are only counted
// under the core.Unknown label, to keep the log readable.
type unlabeledWarner struct {
	logger zerolog.Logger
	warned sync.Map // operation name -> struct{}
}

func (w *unlabeledWarner) warn(operation, path string) {
	if _, seen := w.warned.LoadOrStore(operation, struct{}{}); seen {
		return
	}
	w.logger.Warn().
		Str("operation", operation).
		Str("path", path).
		Str("file_type", core.Unknown.String()).
		Msg("Filesystem operation has no file type; its metrics are labeled Unknown")
}
