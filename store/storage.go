package store

import "os"

// Storage defines the low-level interface for file I/O operations.
// This replaces the old Store interface with a simpler, path-based API.
// Implementations include FSStorage (filesystem) and MemStorage (testing).
type Storage interface {
	// Read returns the contents of the file at path.
	// Returns os.ErrNotExist if the file does not exist.
	Read(path string) ([]byte, error)

	// Write writes data to the file at path, creating directories as needed.
	// Overwrites the file if it already exists.
	Write(path string, data []byte) error

	// Exists returns true if a file exists at path.
	Exists(path string) bool

	// Delete removes the file at path.
	// Returns nil if the file does not exist.
	Delete(path string) error

	// List returns all filenames in dir with the given extension.
	// Returns just the filename (not full path), without the extension.
	// For example, List("games/2024/01/15", "json") might return
	// ["boxscore-2024020001", "daily-schedule-2024-01-15"].
	List(dir string, ext string) ([]string, error)

	// Stat returns file information for the file at path.
	// Returns os.ErrNotExist if the file does not exist.
	// Useful for checking file modification time for cache staleness.
	Stat(path string) (os.FileInfo, error)
}
