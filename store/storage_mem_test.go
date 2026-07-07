package store

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemStorage_ReadWriteExists(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	path := "test/subdir/file.json"
	data := []byte(`{"key": "value"}`)

	// Initially does not exist
	assert.False(t, s.Exists(context.Background(), path))

	// Read returns os.ErrNotExist for non-existent file
	_, err := s.Read(context.Background(), path)
	assert.ErrorIs(t, err, os.ErrNotExist)

	// Write stores data
	err = s.Write(context.Background(), path, data)
	require.NoError(t, err)

	// Now exists
	assert.True(t, s.Exists(context.Background(), path))

	// Read returns the data
	got, err := s.Read(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestMemStorage_WriteOverwrites(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	path := "file.txt"

	err := s.Write(context.Background(), path, []byte("original"))
	require.NoError(t, err)

	err = s.Write(context.Background(), path, []byte("updated"))
	require.NoError(t, err)

	got, err := s.Read(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, []byte("updated"), got)
}

func TestMemStorage_Delete(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

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

func TestMemStorage_List(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	dir := "games/2024/01/15"

	// Create some files
	s.SetFile(dir+"/boxscore-2024020001.json", []byte("{}"))
	s.SetFile(dir+"/boxscore-2024020002.json", []byte("{}"))
	s.SetFile(dir+"/daily-schedule-2024-01-15.json", []byte("{}"))
	s.SetFile(dir+"/readme.txt", []byte("ignored"))    // different extension
	s.SetFile(dir+"/subdir/nested.json", []byte("{}")) // in subdirectory

	// List JSON files
	names, err := s.List(context.Background(), dir, "json")
	require.NoError(t, err)

	// Should be sorted
	assert.Equal(t, []string{
		"boxscore-2024020001",
		"boxscore-2024020002",
		"daily-schedule-2024-01-15",
	}, names)
}

func TestMemStorage_ListEmptyDir(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	// No files in "empty" directory
	names, err := s.List(context.Background(), "empty", "json")
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestMemStorage_SetFile(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	path := "preset/file.json"
	data := []byte(`{"preset": true}`)

	s.SetFile(path, data)

	// Can be read via normal Read
	got, err := s.Read(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestMemStorage_GetAll(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	s.SetFile("a.txt", []byte("a"))
	s.SetFile("b.txt", []byte("b"))

	all := s.GetAll()

	assert.Len(t, all, 2)
	assert.Equal(t, []byte("a"), all["a.txt"])
	assert.Equal(t, []byte("b"), all["b.txt"])
}

func TestMemStorage_Clear(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	s.SetFile("a.txt", []byte("a"))
	s.SetFile("b.txt", []byte("b"))
	assert.Equal(t, 2, s.Count())

	s.Clear()

	assert.Equal(t, 0, s.Count())
	assert.False(t, s.Exists(context.Background(), "a.txt"))
}

func TestMemStorage_Count(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	assert.Equal(t, 0, s.Count())

	s.SetFile("a.txt", []byte("a"))
	assert.Equal(t, 1, s.Count())

	s.SetFile("b.txt", []byte("b"))
	assert.Equal(t, 2, s.Count())

	s.Delete(context.Background(), "a.txt")
	assert.Equal(t, 1, s.Count())
}

func TestMemStorage_Has(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	assert.False(t, s.Has("missing.txt"))

	s.SetFile("present.txt", []byte("here"))
	assert.True(t, s.Has("present.txt"))
}

func TestMemStorage_Get(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	// Returns nil for missing file
	assert.Nil(t, s.Get("missing.txt"))

	s.SetFile("present.txt", []byte("data"))
	assert.Equal(t, []byte("data"), s.Get("present.txt"))
}

func TestMemStorage_ListAll(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	s.SetFile("c.txt", []byte("c"))
	s.SetFile("a.txt", []byte("a"))
	s.SetFile("b.txt", []byte("b"))

	// Should be sorted
	assert.Equal(t, []string{"a.txt", "b.txt", "c.txt"}, s.ListAll())
}

func TestMemStorage_ListDir(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	s.SetFile("dir/a.json", []byte("a"))
	s.SetFile("dir/b.txt", []byte("b"))
	s.SetFile("dir/sub/c.json", []byte("c")) // in subdirectory
	s.SetFile("other/d.json", []byte("d"))   // different directory

	// Lists all files in dir (any extension)
	names := s.ListDir("dir")
	assert.Equal(t, []string{"a.json", "b.txt"}, names)
}

func TestMemStorage_IsolatesData(t *testing.T) {
	t.Parallel()
	s := NewMemStorage()

	original := []byte("original")
	s.Write(context.Background(), "file.txt", original)

	// Modify original slice
	original[0] = 'X'

	// Stored data should be unchanged
	got, _ := s.Read(context.Background(), "file.txt")
	assert.Equal(t, []byte("original"), got)

	// Modify read result
	got[0] = 'Y'

	// Stored data should still be unchanged
	got2, _ := s.Read(context.Background(), "file.txt")
	assert.Equal(t, []byte("original"), got2)
}
