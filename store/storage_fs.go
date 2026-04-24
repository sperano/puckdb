package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// FSStorage implements Storage using the local filesystem.
type FSStorage struct {
	root string
}

// NewFSStorage creates a new FSStorage rooted at the given directory.
func NewFSStorage(root string) *FSStorage {
	return &FSStorage{root: root}
}

// Root returns the root directory of the storage.
func (s *FSStorage) Root() string {
	return s.root
}

// fullPath returns the absolute path for a relative path.
func (s *FSStorage) fullPath(path string) string {
	return filepath.Join(s.root, path)
}

// Read returns the contents of the file at path.
func (s *FSStorage) Read(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadFile(s.fullPath(path))
}

// Write writes data to the file at path, creating directories as needed.
func (s *FSStorage) Write(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full := s.fullPath(path)
	dir := filepath.Dir(full)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(full, data, 0644)
}

// Exists returns true if a file exists at path.
// Ignores ctx.Err() — Exists returns a plain bool, so callers that need
// cancellation should check ctx before calling.
func (s *FSStorage) Exists(_ context.Context, path string) bool {
	_, err := os.Stat(s.fullPath(path))
	return err == nil
}

// Delete removes the file at path.
func (s *FSStorage) Delete(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(s.fullPath(path))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// List returns all filenames in dir with the given extension.
// Returns filenames without the extension.
func (s *FSStorage) List(ctx context.Context, dir string, ext string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.fullPath(dir))
	if err != nil {
		return nil, err
	}

	suffix := "." + ext
	var names []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.HasSuffix(name, suffix) {
			// Strip extension
			names = append(names, name[:len(name)-len(suffix)])
		}
	}

	return names, nil
}

// Stat returns file information for the file at path.
func (s *FSStorage) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.Stat(s.fullPath(path))
}
