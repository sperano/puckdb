package store

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFSStorage_ReadWriteExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	path := "test/subdir/file.json"
	data := []byte(`{"key": "value"}`)

	// Initially does not exist
	assert.False(t, s.Exists(context.Background(), path))

	// Read returns error for non-existent file
	_, err := s.Read(context.Background(), path)
	assert.ErrorIs(t, err, os.ErrNotExist)

	// Write creates directories and file
	err = s.Write(context.Background(), path, data)
	require.NoError(t, err)

	// Now exists
	assert.True(t, s.Exists(context.Background(), path))

	// Read returns the data
	got, err := s.Read(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, data, got)

	// Verify file actually exists on filesystem
	_, err = os.Stat(filepath.Join(root, path))
	assert.NoError(t, err)
}

func TestFSStorage_WriteOverwrites(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	path := "file.txt"

	err := s.Write(context.Background(), path, []byte("original"))
	require.NoError(t, err)

	err = s.Write(context.Background(), path, []byte("updated"))
	require.NoError(t, err)

	got, err := s.Read(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, []byte("updated"), got)
}

func TestFSStorage_Delete(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	path := "to-delete.txt"

	// Delete non-existent file returns nil (not an error)
	err := s.Delete(context.Background(), path)
	assert.NoError(t, err)

	// Create file
	err = s.Write(context.Background(), path, []byte("data"))
	require.NoError(t, err)
	assert.True(t, s.Exists(context.Background(), path))

	// Delete removes it
	err = s.Delete(context.Background(), path)
	assert.NoError(t, err)
	assert.False(t, s.Exists(context.Background(), path))
}

func TestFSStorage_List(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	dir := "games/2024/01/15"

	// Create some files
	require.NoError(t, s.Write(context.Background(), dir+"/boxscore-2024020001.json", []byte("{}")))
	require.NoError(t, s.Write(context.Background(), dir+"/boxscore-2024020002.json", []byte("{}")))
	require.NoError(t, s.Write(context.Background(), dir+"/daily-schedule-2024-01-15.json", []byte("{}")))
	require.NoError(t, s.Write(context.Background(), dir+"/readme.txt", []byte("ignored"))) // different extension

	// List JSON files
	names, err := s.List(context.Background(), dir, "json")
	require.NoError(t, err)

	assert.Len(t, names, 3)
	assert.Contains(t, names, "boxscore-2024020001")
	assert.Contains(t, names, "boxscore-2024020002")
	assert.Contains(t, names, "daily-schedule-2024-01-15")
	assert.NotContains(t, names, "readme")
}

func TestFSStorage_ListEmptyDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	// Create empty directory
	dir := "empty"
	require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))

	names, err := s.List(context.Background(), dir, "json")
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestFSStorage_ListNonExistentDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	_, err := s.List(context.Background(), "does-not-exist", "json")
	assert.Error(t, err)
}

func TestFSStorage_Root(t *testing.T) {
	t.Parallel()
	root := "/some/path"
	s := NewFSStorage(root)
	assert.Equal(t, root, s.Root())
}

// failingTemp wraps a real temp file and fails at a chosen step, so the
// write and close error paths of Write can be exercised with an otherwise
// genuine file on disk.
type failingTemp struct {
	tempFile
	failWrite bool
	failClose bool
}

var errInjected = errors.New("injected failure")

func (f *failingTemp) Write(p []byte) (int, error) {
	if f.failWrite {
		return 0, errInjected
	}
	return f.tempFile.Write(p)
}

func (f *failingTemp) Close() error {
	err := f.tempFile.Close()
	if f.failClose {
		return errInjected
	}
	return err
}

func failAt(failWrite, failClose bool) func(dir, base string) (tempFile, error) {
	return func(dir, base string) (tempFile, error) {
		f, err := createExclusiveTemp(dir, base)
		if err != nil {
			return nil, err
		}
		return &failingTemp{tempFile: f, failWrite: failWrite, failClose: failClose}, nil
	}
}

func listTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if IsTempFile(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestFSStorage_WriteFailurePreservesOldFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		failWrite bool
		failClose bool
	}{
		{name: "write fails", failWrite: true},
		{name: "close fails", failClose: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			s := NewFSStorage(root)
			path := "dir/file.json"

			require.NoError(t, s.Write(context.Background(), path, []byte("old")))

			s.createTemp = failAt(tc.failWrite, tc.failClose)
			err := s.Write(context.Background(), path, []byte("new"))
			require.ErrorIs(t, err, errInjected)

			got, err := s.Read(context.Background(), path)
			require.NoError(t, err)
			assert.Equal(t, []byte("old"), got)
			assert.Empty(t, listTempFiles(t, filepath.Join(root, "dir")))
		})
	}
}

func TestFSStorage_WriteFailureFirstWriteLeavesNoFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)
	s.createTemp = failAt(true, false)
	path := "dir/file.json"

	err := s.Write(context.Background(), path, []byte("data"))
	require.ErrorIs(t, err, errInjected)

	assert.False(t, s.Exists(context.Background(), path))
	assert.Empty(t, listTempFiles(t, filepath.Join(root, "dir")))
}

func TestFSStorage_WriteLeavesNoTempFilesAndSetsMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	require.NoError(t, s.Write(context.Background(), "a/b.json", []byte("x")))
	assert.Empty(t, listTempFiles(t, filepath.Join(root, "a")))

	info, err := os.Stat(filepath.Join(root, "a/b.json"))
	require.NoError(t, err)
	// Mode is filePerm less the process umask, like os.WriteFile produced.
	assert.Equal(t, os.FileMode(filePerm)&^umask(t), info.Mode().Perm())
}

// umask reports the process umask without changing it.
func umask(t *testing.T) os.FileMode {
	t.Helper()
	old := syscall.Umask(0)
	syscall.Umask(old)
	return os.FileMode(old)
}

func TestFSStorage_ConcurrentWritersProduceCompletePayload(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)
	path := "contended.json"

	const (
		writers     = 8
		iterations  = 50
		payloadSize = 64 * 1024
	)
	payloads := make([][]byte, writers)
	for i := range payloads {
		payloads[i] = bytes.Repeat([]byte{byte('a' + i)}, payloadSize)
	}

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(data []byte) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				assert.NoError(t, s.Write(context.Background(), path, data))
			}
		}(payloads[i])
	}
	// A concurrent reader must only ever see one writer's full payload.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iterations*writers; j++ {
			got, err := s.Read(context.Background(), path)
			if errors.Is(err, fs.ErrNotExist) {
				continue // not written yet
			}
			if !assert.NoError(t, err) {
				return
			}
			assert.Contains(t, payloads, got, "reader observed a mixed or truncated payload")
		}
	}()
	wg.Wait()

	got, err := s.Read(context.Background(), path)
	require.NoError(t, err)
	assert.Contains(t, payloads, got)
	assert.Empty(t, listTempFiles(t, root))
}

func TestFSStorage_ListSkipsTempFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)
	dir := "games"

	require.NoError(t, s.Write(context.Background(), dir+"/real.json", []byte("{}")))
	// Simulate a temp file orphaned by a crash mid-write: create one exactly
	// as Write does and never rename it.
	orphan, err := createExclusiveTemp(filepath.Join(root, dir), "real.json")
	require.NoError(t, err)
	require.NoError(t, orphan.Close())
	assert.True(t, IsTempFile(filepath.Base(orphan.Name())))

	names, err := s.List(context.Background(), dir, "json")
	require.NoError(t, err)
	assert.Equal(t, []string{"real"}, names)
}

func TestCreateExclusiveTemp_NameShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, err := createExclusiveTemp(dir, "boxscore.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	name := filepath.Base(f.Name())
	assert.True(t, strings.HasPrefix(name, ".boxscore.json"+tempFileMarker), name)
	assert.True(t, IsTempFile(name), name)
	assert.False(t, strings.HasSuffix(name, ".json"), name)
}

func TestIsTempFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{".boxscore.json.tmp-1a2b3c4d5e6f7a8b", true},
		{".x.tmp-0", true},
		{"boxscore.json", false},
		{".hidden", false},
		{"notes.tmp-old.json", false},  // no leading dot
		{".notes.tmp-old.json", false}, // suffix not hex
		{".notes.tmp-", false},         // empty suffix
		{".notes.tmp-1A2B", false},     // upper-case hex is never produced
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsTempFile(tc.name))
		})
	}
}
