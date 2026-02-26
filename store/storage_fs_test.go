package store

import (
	"os"
	"path/filepath"
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
	assert.False(t, s.Exists(path))

	// Read returns error for non-existent file
	_, err := s.Read(path)
	assert.ErrorIs(t, err, os.ErrNotExist)

	// Write creates directories and file
	err = s.Write(path, data)
	require.NoError(t, err)

	// Now exists
	assert.True(t, s.Exists(path))

	// Read returns the data
	got, err := s.Read(path)
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

	err := s.Write(path, []byte("original"))
	require.NoError(t, err)

	err = s.Write(path, []byte("updated"))
	require.NoError(t, err)

	got, err := s.Read(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("updated"), got)
}

func TestFSStorage_Delete(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	path := "to-delete.txt"

	// Delete non-existent file returns nil (not an error)
	err := s.Delete(path)
	assert.NoError(t, err)

	// Create file
	err = s.Write(path, []byte("data"))
	require.NoError(t, err)
	assert.True(t, s.Exists(path))

	// Delete removes it
	err = s.Delete(path)
	assert.NoError(t, err)
	assert.False(t, s.Exists(path))
}

func TestFSStorage_List(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	dir := "games/2024/01/15"

	// Create some files
	require.NoError(t, s.Write(dir+"/boxscore-2024020001.json", []byte("{}")))
	require.NoError(t, s.Write(dir+"/boxscore-2024020002.json", []byte("{}")))
	require.NoError(t, s.Write(dir+"/daily-schedule-2024-01-15.json", []byte("{}")))
	require.NoError(t, s.Write(dir+"/readme.txt", []byte("ignored"))) // different extension

	// List JSON files
	names, err := s.List(dir, "json")
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

	names, err := s.List(dir, "json")
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestFSStorage_ListNonExistentDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := NewFSStorage(root)

	_, err := s.List("does-not-exist", "json")
	assert.Error(t, err)
}

func TestFSStorage_Root(t *testing.T) {
	t.Parallel()
	root := "/some/path"
	s := NewFSStorage(root)
	assert.Equal(t, root, s.Root())
}
