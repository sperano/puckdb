package store

import (
	"context"
	"os"

	"github.com/rs/zerolog/log"
)

// Storage defines the low-level interface for file I/O operations.
// Every method accepts a context so callers can cancel long-running I/O —
// important because production FSStorage is backed by a JuiceFS mount where
// individual filesystem calls can block on network hiccups.
// Implementations include FSStorage (filesystem) and MemStorage (testing).
type Storage interface {
	// Read returns the contents of the file at path.
	// Returns os.ErrNotExist if the file does not exist.
	Read(ctx context.Context, path string) ([]byte, error)

	// Write writes data to the file at path, creating directories as needed.
	// Overwrites the file if it already exists.
	Write(ctx context.Context, path string, data []byte) error

	// Exists returns true if a file exists at path.
	Exists(ctx context.Context, path string) bool

	// Delete removes the file at path.
	// Returns nil if the file does not exist.
	Delete(ctx context.Context, path string) error

	// List returns all filenames in dir with the given extension.
	// Returns just the filename (not full path), without the extension.
	// For example, List(ctx, "games/2024/01/15", "json") might return
	// ["boxscore-2024020001", "daily-schedule-2024-01-15"].
	List(ctx context.Context, dir string, ext string) ([]string, error)

	// Stat returns file information for the file at path.
	// Returns os.ErrNotExist if the file does not exist.
	// Useful for checking file modification time for cache staleness.
	Stat(ctx context.Context, path string) (os.FileInfo, error)
}

// NewDefaultStorage creates the filesystem Storage rooted at dataPath,
// instrumented for metrics collection.
func NewDefaultStorage(dataPath string) Storage {
	log.Trace().Str("path", dataPath).Msg("Initializing storage")
	return NewInstrumentedStorage(NewFSStorage(dataPath))
}
