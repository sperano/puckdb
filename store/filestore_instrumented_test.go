package store

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockStore implements Store for testing
type MockStore struct {
	mock.Mock
}

func (m *MockStore) Read(file File) ([]byte, error) {
	args := m.Called(file)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockStore) Write(file File, content []byte) error {
	args := m.Called(file, content)
	return args.Error(0)
}

func (m *MockStore) Exists(file File) bool {
	args := m.Called(file)
	return args.Bool(0)
}

func (m *MockStore) MkdirAll(dir string, perm os.FileMode) error {
	args := m.Called(dir, perm)
	return args.Error(0)
}

func (m *MockStore) Remove(file File) error {
	args := m.Called(file)
	return args.Error(0)
}

func (m *MockStore) FullPath(file File) string {
	args := m.Called(file)
	return args.String(0)
}

func (m *MockStore) ListFiles(sample File, parser FilenameParser) ([]File, error) {
	args := m.Called(sample, parser)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]File), args.Error(1)
}

func TestNewInstrumentedStore(t *testing.T) {
	t.Parallel()

	inner := &MockStore{}
	instrumented := NewInstrumentedStore(inner)

	assert.NotNil(t, instrumented)
	assert.Equal(t, inner, instrumented.inner)
}

func TestFileTypeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		file     File
		expected string
	}{
		{
			name:     "DailyScheduleFile",
			file:     DailyScheduleFile{},
			expected: "DailyScheduleFile",
		},
		{
			name:     "BoxscoreFile",
			file:     BoxscoreFile{},
			expected: "BoxscoreFile",
		},
		{
			name:     "YahooPlayerFile",
			file:     YahooPlayerFile{PlayerID: 123},
			expected: "YahooPlayerFile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fileTypeName(tt.file)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInstrumentedStore_Read(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		inner := &MockStore{}
		file := testFile{dir: "dir", name: "file", ext: "json"}
		expectedData := []byte("content")

		inner.On("Read", file).Return(expectedData, nil)
		instrumented := NewInstrumentedStore(inner)

		data, err := instrumented.Read(file)
		require.NoError(t, err)
		assert.Equal(t, expectedData, data)
		inner.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		inner := &MockStore{}
		file := testFile{dir: "dir", name: "file", ext: "json"}

		inner.On("Read", file).Return(nil, os.ErrNotExist)
		instrumented := NewInstrumentedStore(inner)

		_, err := instrumented.Read(file)
		assert.Error(t, err)
		inner.AssertExpectations(t)
	})
}

func TestInstrumentedStore_Write(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		inner := &MockStore{}
		file := testFile{dir: "dir", name: "file", ext: "json"}
		content := []byte("content")

		inner.On("Write", file, content).Return(nil)
		instrumented := NewInstrumentedStore(inner)

		err := instrumented.Write(file, content)
		require.NoError(t, err)
		inner.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		inner := &MockStore{}
		file := testFile{dir: "dir", name: "file", ext: "json"}
		content := []byte("content")

		inner.On("Write", file, content).Return(os.ErrPermission)
		instrumented := NewInstrumentedStore(inner)

		err := instrumented.Write(file, content)
		assert.Error(t, err)
		inner.AssertExpectations(t)
	})
}

func TestInstrumentedStore_Exists(t *testing.T) {
	t.Parallel()

	inner := &MockStore{}
	file := testFile{dir: "dir", name: "file", ext: "json"}

	inner.On("Exists", file).Return(true)
	instrumented := NewInstrumentedStore(inner)

	exists := instrumented.Exists(file)
	assert.True(t, exists)
	inner.AssertExpectations(t)
}

func TestInstrumentedStore_MkdirAll(t *testing.T) {
	t.Parallel()

	inner := &MockStore{}
	inner.On("MkdirAll", "some/dir", os.FileMode(0755)).Return(nil)
	instrumented := NewInstrumentedStore(inner)

	err := instrumented.MkdirAll("some/dir", 0755)
	require.NoError(t, err)
	inner.AssertExpectations(t)
}

func TestInstrumentedStore_Remove(t *testing.T) {
	t.Parallel()

	inner := &MockStore{}
	file := testFile{dir: "dir", name: "file", ext: "json"}

	inner.On("Remove", file).Return(nil)
	instrumented := NewInstrumentedStore(inner)

	err := instrumented.Remove(file)
	require.NoError(t, err)
	inner.AssertExpectations(t)
}

func TestInstrumentedStore_FullPath(t *testing.T) {
	t.Parallel()

	inner := &MockStore{}
	file := testFile{dir: "dir", name: "file", ext: "json"}

	inner.On("FullPath", file).Return("/data/dir/file.json")
	instrumented := NewInstrumentedStore(inner)

	path := instrumented.FullPath(file)
	assert.Equal(t, "/data/dir/file.json", path)
	inner.AssertExpectations(t)
}

func TestInstrumentedStore_ListFiles(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		inner := &MockStore{}
		sample := YahooPlayerFile{}
		expected := []File{
			YahooPlayerFile{PlayerID: 1},
			YahooPlayerFile{PlayerID: 2},
		}

		inner.On("ListFiles", sample, mock.AnythingOfType("FilenameParser")).Return(expected, nil)
		instrumented := NewInstrumentedStore(inner)

		files, err := instrumented.ListFiles(sample, ParseYahooPlayerFilename)
		require.NoError(t, err)
		assert.Equal(t, expected, files)
		inner.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		inner := &MockStore{}
		sample := YahooPlayerFile{}

		inner.On("ListFiles", sample, mock.AnythingOfType("FilenameParser")).Return(nil, os.ErrNotExist)
		instrumented := NewInstrumentedStore(inner)

		_, err := instrumented.ListFiles(sample, ParseYahooPlayerFilename)
		assert.Error(t, err)
		inner.AssertExpectations(t)
	})
}

