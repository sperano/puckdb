package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testFile is a minimal File implementation for testing
type testFile struct {
	dir  string
	name string
	ext  string
}

func (f testFile) Dir() string  { return f.dir }
func (f testFile) Name() string { return f.name }
func (f testFile) Ext() string  { return f.ext }

func TestPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		file     File
		expected string
	}{
		{
			name:     "simple file",
			file:     testFile{dir: "data", name: "test", ext: "json"},
			expected: "data/test.json",
		},
		{
			name:     "nested directory",
			file:     testFile{dir: "2024/games/01/15", name: "boxscore-123", ext: "json"},
			expected: "2024/games/01/15/boxscore-123.json",
		},
		{
			name:     "xml extension",
			file:     testFile{dir: "league", name: "league", ext: "xml"},
			expected: "league/league.xml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Path(tt.file)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewFileStore(t *testing.T) {
	t.Parallel()

	fs := NewFileStore("/tmp/test-root")
	assert.Equal(t, "/tmp/test-root", fs.RootPath)
}

func TestFileStore_FullPath(t *testing.T) {
	t.Parallel()

	fs := NewFileStore("/data")
	file := testFile{dir: "games/2024", name: "schedule", ext: "json"}

	result := fs.FullPath(file)
	assert.Equal(t, "/data/games/2024/schedule.json", result)
}

func TestFileStore_ReadWriteExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)
	file := testFile{dir: "subdir", name: "test", ext: "txt"}

	// File should not exist initially
	assert.False(t, fs.Exists(file))

	// Write content
	content := []byte("hello world")
	err := fs.Write(file, content)
	require.NoError(t, err)

	// File should exist now
	assert.True(t, fs.Exists(file))

	// Read content back
	data, err := fs.Read(file)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestFileStore_ReadError(t *testing.T) {
	t.Parallel()

	fs := NewFileStore("/nonexistent")
	file := testFile{dir: "missing", name: "file", ext: "txt"}

	_, err := fs.Read(file)
	assert.Error(t, err)
}

func TestFileStore_WriteError_MkdirFails(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Create a file where a directory should be, blocking MkdirAll
	blockingFile := filepath.Join(tmpDir, "blocking")
	err := os.WriteFile(blockingFile, []byte("block"), 0644)
	require.NoError(t, err)

	fs := NewFileStore(tmpDir)
	file := testFile{dir: "blocking/subdir", name: "test", ext: "txt"}

	err = fs.Write(file, []byte("content"))
	assert.Error(t, err)
}

func TestFileStore_MkdirAll(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	err := fs.MkdirAll("deep/nested/dir", 0755)
	require.NoError(t, err)

	// Verify directory was created
	info, err := os.Stat(filepath.Join(tmpDir, "deep/nested/dir"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestFileStore_Remove(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)
	file := testFile{dir: ".", name: "toremove", ext: "txt"}

	// Create a file
	err := fs.Write(file, []byte("content"))
	require.NoError(t, err)
	assert.True(t, fs.Exists(file))

	// Remove it
	err = fs.Remove(file)
	require.NoError(t, err)
	assert.False(t, fs.Exists(file))
}

func TestFileStore_Remove_NonExistent(t *testing.T) {
	t.Parallel()

	fs := NewFileStore(t.TempDir())
	file := testFile{dir: ".", name: "nonexistent", ext: "txt"}

	err := fs.Remove(file)
	assert.Error(t, err)
}

func TestListAll(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	// Use sample to get the correct directory name
	sample := YahooPlayerFile{PlayerID: 0}

	// Create test directory and files using sample's Dir()
	testDir := filepath.Join(tmpDir, sample.Dir())
	err := os.MkdirAll(testDir, 0755)
	require.NoError(t, err)

	// Create matching files
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "player-1.html"), []byte("p1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "player-2.html"), []byte("p2"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "player-abc.html"), []byte("invalid"), 0644)) // Invalid ID

	// Create non-matching files
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "other.json"), []byte("json"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "noext"), []byte("no ext"), 0644))

	// Create a subdirectory (should be skipped)
	require.NoError(t, os.MkdirAll(filepath.Join(testDir, "subdir"), 0755))

	files, err := ListAll(fs, sample, ParseYahooPlayerFilename)
	require.NoError(t, err)

	// Should find player-1 and player-2, but not player-abc (invalid) or other.json (wrong ext)
	assert.Len(t, files, 2)

	// Check that we got the right files
	var ids []int
	for _, f := range files {
		if yp, ok := f.(YahooPlayerFile); ok {
			ids = append(ids, yp.PlayerID)
		}
	}
	assert.Contains(t, ids, 1)
	assert.Contains(t, ids, 2)
}

func TestListAll_DirectoryNotExists(t *testing.T) {
	t.Parallel()

	fs := NewFileStore(t.TempDir())
	sample := YahooPlayerFile{PlayerID: 0}

	_, err := ListAll(fs, sample, ParseYahooPlayerFilename)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read dir")
}

func TestListAll_ShortFilename(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)
	sample := YahooPlayerFile{PlayerID: 0}

	// Create directory with short filename that matches extension length
	testDir := filepath.Join(tmpDir, sample.Dir())
	err := os.MkdirAll(testDir, 0755)
	require.NoError(t, err)

	// Create files with names that are too short
	require.NoError(t, os.WriteFile(filepath.Join(testDir, ".html"), []byte("too short"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "x.html"), []byte("also short"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "player-1.html"), []byte("valid"), 0644))

	files, err := ListAll(fs, sample, ParseYahooPlayerFilename)
	require.NoError(t, err)

	// Only player-1.html should be found (x.html doesn't match parser pattern)
	assert.Len(t, files, 1)
}
