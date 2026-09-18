package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
)

// tempFileMarker separates the destination name from the random suffix in
// in-flight write files, which look like ".boxscore-2024020001.json.tmp-1a2b3c4d5e6f7a8b".
// Directory scanners (List, the metrics indexer) rely on this shape to skip
// them, so real cache filenames must never start with "." and contain ".tmp-".
const tempFileMarker = ".tmp-"

// tempCreateAttempts bounds the O_EXCL retry loop in case of a collision.
const tempCreateAttempts = 10

const (
	dirPerm  = 0755
	filePerm = 0644
)

// IsTempFile reports whether name is an in-flight write staged by
// FSStorage.Write. Such files only outlive Write when the process crashes
// between creation and rename; readers must treat them as absent.
func IsTempFile(name string) bool {
	if !strings.HasPrefix(name, ".") {
		return false
	}
	_, suffix, found := strings.Cut(name, tempFileMarker)
	return found && suffix != "" && isHex(suffix)
}

func isHex(s string) bool {
	for _, r := range s {
		if !(('0' <= r && r <= '9') || ('a' <= r && r <= 'f')) {
			return false
		}
	}
	return true
}

// tempFile is the staging file Write fills before renaming it into place.
// *os.File satisfies it; tests wrap one to inject write/close failures.
type tempFile interface {
	io.WriteCloser
	Name() string
}

// FSStorage implements Storage using the local filesystem.
type FSStorage struct {
	root string
	// createTemp opens the staging file for a write to dir/base. It defaults
	// to createExclusiveTemp; tests substitute it to inject failures.
	createTemp func(dir, base string) (tempFile, error)
}

// NewFSStorage creates a new FSStorage rooted at the given directory.
func NewFSStorage(root string) *FSStorage {
	return &FSStorage{root: root, createTemp: createExclusiveTemp}
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

// Write atomically replaces the file at path with data, creating directories
// as needed.
//
// Data is staged into a uniquely named temp file in the destination directory
// and renamed into place only after every write and the close succeed. Readers
// therefore observe either the previous complete file or the new complete
// file, never a truncated or interleaved one, and a failed write leaves the
// previous file untouched (or no file at all for a first write). Concurrent
// writers each stage their own temp file; the last rename wins with a complete
// payload.
//
// The temp file is deliberately not fsync'd: this storage is a cache of
// re-downloadable API responses, so the guarantee is atomic visibility to
// concurrent and subsequent readers, not durability across power loss.
//
// A process crash between temp creation and rename orphans the temp file.
// Scanners skip it (see IsTempFile) but nothing reaps it; that is accepted
// as rare and harmless beyond the disk space it occupies.
func (s *FSStorage) Write(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full := s.fullPath(path)
	dir := filepath.Dir(full)

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	tmpName, err := s.stageTempFile(dir, filepath.Base(full), data)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return errors.Join(err, removeIfPresent(tmpName))
	}
	return nil
}

// stageTempFile writes data to a fresh temp file in dir and returns its name.
// On any failure the temp file is removed and the error returned.
func (s *FSStorage) stageTempFile(dir, base string, data []byte) (string, error) {
	create := s.createTemp
	if create == nil {
		create = createExclusiveTemp
	}
	f, err := create(dir, base)
	if err != nil {
		return "", fmt.Errorf("create temp file for %s: %w", base, err)
	}
	name := f.Name()

	if _, err := f.Write(data); err != nil {
		return "", errors.Join(fmt.Errorf("write temp file for %s: %w", base, err), f.Close(), removeIfPresent(name))
	}
	if err := f.Close(); err != nil {
		return "", errors.Join(fmt.Errorf("close temp file for %s: %w", base, err), removeIfPresent(name))
	}
	return name, nil
}

// createExclusiveTemp creates ".<base>.tmp-<random hex>" in dir with filePerm
// (subject to umask, exactly as os.WriteFile was). The 64-bit random suffix
// makes a collision between concurrent writers of the same path practically
// impossible, and O_EXCL guarantees the name is ours regardless. Unlike
// os.CreateTemp this needs no follow-up chmod, which matters on JuiceFS where
// every metadata call is a round trip.
func createExclusiveTemp(dir, base string) (tempFile, error) {
	for attempt := 0; attempt < tempCreateAttempts; attempt++ {
		name := filepath.Join(dir, fmt.Sprintf(".%s%s%016x", base, tempFileMarker, rand.Uint64()))
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	return nil, fmt.Errorf("no free temp name for %s after %d attempts", base, tempCreateAttempts)
}

// removeIfPresent deletes name, treating a missing file as success.
func removeIfPresent(name string) error {
	err := os.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
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
	if errors.Is(err, fs.ErrNotExist) {
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
		// Temp files end in a hex suffix so never match an extension, but
		// keep the skip explicit rather than rely on that coincidence.
		if IsTempFile(name) {
			continue
		}
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
