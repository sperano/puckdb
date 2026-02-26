package store

import (
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
func (s *FSStorage) Read(path string) ([]byte, error) {
	return os.ReadFile(s.fullPath(path))
}

// Write writes data to the file at path, creating directories as needed.
func (s *FSStorage) Write(path string, data []byte) error {
	full := s.fullPath(path)
	dir := filepath.Dir(full)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(full, data, 0644)
}

// Exists returns true if a file exists at path.
func (s *FSStorage) Exists(path string) bool {
	_, err := os.Stat(s.fullPath(path))
	return err == nil
}

// Delete removes the file at path.
func (s *FSStorage) Delete(path string) error {
	err := os.Remove(s.fullPath(path))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// List returns all filenames in dir with the given extension.
// Returns filenames without the extension.
func (s *FSStorage) List(dir string, ext string) ([]string, error) {
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
func (s *FSStorage) Stat(path string) (os.FileInfo, error) {
	return os.Stat(s.fullPath(path))
}
