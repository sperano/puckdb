package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sperano/yfh/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestGenerator_ProducesAllItems(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	ids := []int{1, 2, 3, 4, 5}

	out := Generator[int, string](ctx, day, ids)

	var items []*PipelineItem[int, string]
	for item := range out {
		items = append(items, item)
	}

	assert.Len(t, items, 5)
	for i, item := range items {
		assert.Equal(t, day, item.Day)
		assert.Equal(t, ids[i], item.ID)
	}
}

func TestGenerator_EmptyInput(t *testing.T) {
	ctx := context.Background()
	day := time.Now()
	ids := []int{}

	out := Generator[int, string](ctx, day, ids)

	var items []*PipelineItem[int, string]
	for item := range out {
		items = append(items, item)
	}

	assert.Len(t, items, 0)
}

func TestGenerator_RespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	day := time.Now()
	ids := make([]int, 1000) // Large number of IDs
	for i := range ids {
		ids[i] = i
	}

	out := Generator[int, string](ctx, day, ids)

	// Read one item
	<-out

	// Cancel context
	cancel()

	// Channel should close eventually
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return // Success: channel closed
			}
		case <-timeout:
			t.Fatal("timeout waiting for generator to close after cancel")
		}
	}
}

func TestGenerator_PreservesOrderAndDayInfo(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	ids := []string{"a", "b", "c"}

	out := Generator[string, int](ctx, day, ids)

	var results []string
	for item := range out {
		assert.Equal(t, day, item.Day)
		results = append(results, item.ID)
	}

	assert.Equal(t, ids, results)
}

func TestPipelineItem_Structure(t *testing.T) {
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	item := &PipelineItem[int, string]{
		Day:    day,
		ID:     42,
		Parsed: "parsed content",
	}

	assert.Equal(t, day, item.Day)
	assert.Equal(t, 42, item.ID)
	assert.Equal(t, "parsed content", item.Parsed)
}

func TestRoutinesPerStageConstant(t *testing.T) {
	assert.Equal(t, 4, RoutinesPerStage)
}

func TestRecoverPanic_NoPanic(t *testing.T) {
	errc := make(chan error, 1)
	func() {
		defer recoverPanic(errc, "test")
		// No panic
	}()
	close(errc)

	select {
	case err := <-errc:
		assert.Nil(t, err)
	default:
		// No error sent, as expected
	}
}

func TestRecoverPanic_WithPanic(t *testing.T) {
	errc := make(chan error, 1)
	func() {
		defer recoverPanic(errc, "test-stage")
		panic("test panic")
	}()

	err := <-errc
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "panic in test-stage")
	assert.Contains(t, err.Error(), "test panic")
}

func TestPipelineConfig_getFS_Default(t *testing.T) {
	config := PipelineConfig[int, string]{
		FileType: 0,
	}

	fs := config.getFS()
	assert.NotNil(t, fs)
}

func TestPipelineConfig_getFS_Custom(t *testing.T) {
	mockFS := NewMockFileSystem()
	config := PipelineConfig[int, string]{
		FileType: 0,
		FSFactory: func() cache.FileSystem {
			return mockFS
		},
	}

	fs := config.getFS()
	assert.Equal(t, mockFS, fs)
}

func TestRunDownloadPipeline_EmptyIDs(t *testing.T) {
	ctx := context.Background()
	day := time.Now()
	config := PipelineConfig[int, string]{}

	err := RunDownloadPipeline(ctx, config, day, []int{})

	assert.NoError(t, err)
}

func TestRunImportPipeline_EmptyIDs(t *testing.T) {
	ctx := context.Background()
	day := time.Now()
	config := PipelineConfig[int, string]{}

	err := RunImportPipeline(ctx, config, day, []int{})

	assert.NoError(t, err)
}

func TestDownloadStage_FileAlreadyExists(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "1", ExtVal: "html"}

	mockFS.On("New", cache.FileType(0), mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(true)

	config := PipelineConfig[int, string]{
		FileType: 0,
		IDName:   func(id int) string { return fmt.Sprintf("%d", id) },
		FSFactory: func() cache.FileSystem {
			return mockFS
		},
	}

	// Create input channel
	in := make(chan *PipelineItem[int, string], 1)
	in <- &PipelineItem[int, string]{Day: day, ID: 1}
	close(in)

	// Run stage
	stageFn := DownloadStage(config)
	out, errc := stageFn(ctx, in)

	// Collect results
	var items []*PipelineItem[int, string]
	for item := range out {
		items = append(items, item)
	}

	// Check for errors
	for err := range errc {
		assert.NoError(t, err)
	}

	assert.Len(t, items, 1)
	assert.Equal(t, 1, items[0].ID)
	mockFS.AssertExpectations(t)
}

func TestDownloadStage_DownloadsAndSavesFile(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "1", ExtVal: "html"}

	mockFS.On("New", cache.FileType(0), mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(false)
	mockFS.On("Write", mockFile, []byte("downloaded content")).Return(nil)

	config := PipelineConfig[int, string]{
		FileType: 0,
		IDName:   func(id int) string { return fmt.Sprintf("%d", id) },
		Downloader: func(id int) ([]byte, error) {
			return []byte("downloaded content"), nil
		},
		FSFactory: func() cache.FileSystem {
			return mockFS
		},
	}

	// Create input channel
	in := make(chan *PipelineItem[int, string], 1)
	in <- &PipelineItem[int, string]{Day: day, ID: 1}
	close(in)

	// Run stage
	stageFn := DownloadStage(config)
	out, errc := stageFn(ctx, in)

	// Collect results
	var items []*PipelineItem[int, string]
	for item := range out {
		items = append(items, item)
	}

	// Check for errors
	for err := range errc {
		assert.NoError(t, err)
	}

	assert.Len(t, items, 1)
	mockFS.AssertExpectations(t)
}

func TestDownloadStage_MkdirAllError(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "1", ExtVal: "html"}

	mockFS.On("New", cache.FileType(0), mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(errors.New("mkdir failed"))

	config := PipelineConfig[int, string]{
		FileType: 0,
		IDName:   func(id int) string { return fmt.Sprintf("%d", id) },
		FSFactory: func() cache.FileSystem {
			return mockFS
		},
	}

	// Create input channel
	in := make(chan *PipelineItem[int, string], 1)
	in <- &PipelineItem[int, string]{Day: day, ID: 1}
	close(in)

	// Run stage
	stageFn := DownloadStage(config)
	out, errc := stageFn(ctx, in)

	// Drain output
	for range out {
	}

	// Check for error
	var gotError error
	for err := range errc {
		gotError = err
	}

	assert.Error(t, gotError)
	assert.Contains(t, gotError.Error(), "mkdir failed")
}

func TestParseStage_ParsesSuccessfully(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "1", ExtVal: "html"}

	mockFS.On("New", cache.FileType(0), mock.Anything).Return(mockFile)

	config := PipelineConfig[int, string]{
		FileType: 0,
		IDName:   func(id int) string { return fmt.Sprintf("%d", id) },
		Parser: func(fs cache.FileSystem, file cache.File) (string, error) {
			return "parsed result", nil
		},
		FSFactory: func() cache.FileSystem {
			return mockFS
		},
	}

	// Create input channel
	in := make(chan *PipelineItem[int, string], 1)
	in <- &PipelineItem[int, string]{Day: day, ID: 1}
	close(in)

	// Run stage
	stageFn := ParseStage(config)
	out, errc := stageFn(ctx, in)

	// Collect results
	var items []*PipelineItem[int, string]
	for item := range out {
		items = append(items, item)
	}

	// Check for errors
	for err := range errc {
		assert.NoError(t, err)
	}

	assert.Len(t, items, 1)
	assert.Equal(t, "parsed result", items[0].Parsed)
}

func TestParseStage_ParseError(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "1", ExtVal: "html"}

	mockFS.On("New", cache.FileType(0), mock.Anything).Return(mockFile)

	config := PipelineConfig[int, string]{
		FileType: 0,
		IDName:   func(id int) string { return fmt.Sprintf("%d", id) },
		Parser: func(fs cache.FileSystem, file cache.File) (string, error) {
			return "", errors.New("parse error")
		},
		FSFactory: func() cache.FileSystem {
			return mockFS
		},
	}

	// Create input channel
	in := make(chan *PipelineItem[int, string], 1)
	in <- &PipelineItem[int, string]{Day: day, ID: 1}
	close(in)

	// Run stage
	stageFn := ParseStage(config)
	out, errc := stageFn(ctx, in)

	// Drain output
	for range out {
	}

	// Check for error
	var gotError error
	for err := range errc {
		gotError = err
	}

	assert.Error(t, gotError)
	assert.Contains(t, gotError.Error(), "parse error")
}
