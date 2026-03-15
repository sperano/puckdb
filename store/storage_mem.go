package store

import (
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemStorage implements Storage using an in-memory map.
// This is the primary test double for all storage operations,
// replacing the old MockStore pattern.
type MemStorage struct {
	mu    sync.RWMutex
	files map[string][]byte
	times map[string]time.Time
}

// NewMemStorage creates a new empty MemStorage.
func NewMemStorage() *MemStorage {
	return &MemStorage{
		files: make(map[string][]byte),
		times: make(map[string]time.Time),
	}
}

// Read returns the contents of the file at path.
func (s *MemStorage) Read(p string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, ok := s.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}

	// Return a copy to prevent mutation
	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// Write stores data at path.
func (s *MemStorage) Write(p string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Store a copy to prevent mutation
	stored := make([]byte, len(data))
	copy(stored, data)
	s.files[p] = stored
	s.times[p] = time.Now()
	return nil
}

// Exists returns true if a file exists at path.
func (s *MemStorage) Exists(p string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.files[p]
	return ok
}

// Delete removes the file at path.
func (s *MemStorage) Delete(p string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.files, p)
	delete(s.times, p)
	return nil
}

// List returns all filenames in dir with the given extension.
// Returns filenames without the extension.
func (s *MemStorage) List(dir string, ext string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := dir + "/"
	suffix := "." + ext

	var names []string
	for p := range s.files {
		// Must be in the correct directory (not subdirectories)
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		// Extract relative path from directory
		rel := p[len(prefix):]
		// Skip if it's in a subdirectory
		if strings.Contains(rel, "/") {
			continue
		}
		// Must have the correct extension
		if !strings.HasSuffix(rel, suffix) {
			continue
		}
		// Strip extension
		name := rel[:len(rel)-len(suffix)]
		names = append(names, name)
	}
	// Sort for deterministic output
	sort.Strings(names)
	return names, nil
}

// SetFile is a test helper to pre-populate storage.
// This is the preferred way to set up test fixtures.
func (s *MemStorage) SetFile(p string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := make([]byte, len(data))
	copy(stored, data)
	s.files[p] = stored
	s.times[p] = time.Now()
}

// GetAll returns a copy of all stored files.
// Useful for test assertions.
func (s *MemStorage) GetAll() map[string][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]byte, len(s.files))
	for k, v := range s.files {
		data := make([]byte, len(v))
		copy(data, v)
		result[k] = data
	}
	return result
}

// Clear removes all files from storage.
func (s *MemStorage) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.files = make(map[string][]byte)
	s.times = make(map[string]time.Time)
}

// Count returns the number of stored files.
func (s *MemStorage) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.files)
}

// Has returns true if a file exists at the exact path.
// Alias for Exists, provided for test readability.
func (s *MemStorage) Has(p string) bool {
	return s.Exists(p)
}

// Get returns the contents of a file, or nil if not found.
// Alias for Read without error, provided for test assertions.
func (s *MemStorage) Get(p string) []byte {
	data, _ := s.Read(p)
	return data
}

// ListAll returns all stored paths sorted alphabetically.
func (s *MemStorage) ListAll() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	paths := make([]string, 0, len(s.files))
	for p := range s.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// ListDir returns all files in the given directory (non-recursive).
func (s *MemStorage) ListDir(dir string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := dir + "/"
	var names []string

	for p := range s.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rel := p[len(prefix):]
		if strings.Contains(rel, "/") {
			continue
		}
		names = append(names, path.Base(p))
	}

	sort.Strings(names)
	return names
}

// Stat returns file information for the file at path.
func (s *MemStorage) Stat(p string) (os.FileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, ok := s.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}

	modTime := s.times[p]
	if modTime.IsZero() {
		modTime = time.Now()
	}

	return &memFileInfo{
		name:    path.Base(p),
		size:    int64(len(data)),
		modTime: modTime,
	}, nil
}

// SetFileWithTime is a test helper to pre-populate storage with a specific modification time.
// Useful for testing staleness checks.
func (s *MemStorage) SetFileWithTime(p string, data []byte, modTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := make([]byte, len(data))
	copy(stored, data)
	s.files[p] = stored
	s.times[p] = modTime
}

// memFileInfo implements os.FileInfo for MemStorage.
type memFileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (f *memFileInfo) Name() string       { return f.name }
func (f *memFileInfo) Size() int64        { return f.size }
func (f *memFileInfo) Mode() fs.FileMode  { return 0644 }
func (f *memFileInfo) ModTime() time.Time { return f.modTime }
func (f *memFileInfo) IsDir() bool        { return false }
func (f *memFileInfo) Sys() any           { return nil }
