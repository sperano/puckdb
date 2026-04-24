package resource_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/resource"
)

// mockStorage is a minimal in-memory implementation of store.Storage for testing.
type mockStorage struct {
	data     map[string][]byte
	readErr  error
	writeErr error
}

func newMockStorage() *mockStorage {
	return &mockStorage{data: make(map[string][]byte)}
}

func (m *mockStorage) Read(_ context.Context, path string) ([]byte, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	data, ok := m.data[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *mockStorage) Write(_ context.Context, path string, data []byte) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.data[path] = data
	return nil
}

func (m *mockStorage) Exists(_ context.Context, path string) bool {
	_, ok := m.data[path]
	return ok
}

func (m *mockStorage) Delete(_ context.Context, path string) error {
	delete(m.data, path)
	return nil
}

func (m *mockStorage) List(_ context.Context, _ string, _ string) ([]string, error) {
	return nil, nil
}

func (m *mockStorage) Stat(_ context.Context, path string) (os.FileInfo, error) {
	if _, ok := m.data[path]; !ok {
		return nil, os.ErrNotExist
	}
	return nil, nil
}

func TestReadParsed(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	r := resource.DailySchedule{Date: date}

	t.Run("success", func(t *testing.T) {
		s := newMockStorage()
		data := mustMarshal(&nhl.DailySchedule{})
		s.data[r.Path()] = data

		result, err := resource.ReadParsed(context.Background(), s, r)
		if err != nil {
			t.Fatalf("ReadParsed() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("ReadParsed() returned nil")
		}
	})

	t.Run("storage_read_error", func(t *testing.T) {
		sentinel := errors.New("storage unavailable")
		s := newMockStorage()
		s.readErr = sentinel

		_, err := resource.ReadParsed(context.Background(), s, r)
		if !errors.Is(err, sentinel) {
			t.Errorf("ReadParsed() error = %v, want sentinel error", err)
		}
	})

	t.Run("file_not_found", func(t *testing.T) {
		s := newMockStorage()
		// Nothing written — Read will return os.ErrNotExist.

		_, err := resource.ReadParsed(context.Background(), s, r)
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("ReadParsed() error = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("invalid_data_parse_error", func(t *testing.T) {
		s := newMockStorage()
		s.data[r.Path()] = []byte(`not-json`)

		_, err := resource.ReadParsed(context.Background(), s, r)
		if err == nil {
			t.Fatal("ReadParsed() expected parse error, got nil")
		}
	})
}

func TestWriteParsed(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	r := resource.DailySchedule{Date: date}

	t.Run("success_writes_correct_path", func(t *testing.T) {
		s := newMockStorage()

		if err := resource.WriteParsed(context.Background(), s, r, &nhl.DailySchedule{}); err != nil {
			t.Fatalf("WriteParsed() unexpected error: %v", err)
		}
		if _, ok := s.data[r.Path()]; !ok {
			t.Errorf("WriteParsed() did not write to path %q", r.Path())
		}
	})

	t.Run("format_error_propagated", func(t *testing.T) {
		// nhl.ClubStats has a GameType with a strict marshaler that rejects its
		// zero value, so passing a zero-value ClubStats exercises the Format error path.
		s := newMockStorage()
		cr := resource.ClubStatsResource{Season: 2024, TeamAbbrev: "MTL", GameType: 2}
		err := resource.WriteParsed(context.Background(), s, cr, &nhl.ClubStats{}) // zero GameType → marshal error
		if err == nil {
			t.Fatal("WriteParsed() expected format error, got nil")
		}
	})

	t.Run("storage_write_error", func(t *testing.T) {
		sentinel := errors.New("disk full")
		s := newMockStorage()
		s.writeErr = sentinel

		err := resource.WriteParsed(context.Background(), s, r, &nhl.DailySchedule{})
		if !errors.Is(err, sentinel) {
			t.Errorf("WriteParsed() error = %v, want sentinel error", err)
		}
	})

	t.Run("round_trip_via_read_parsed", func(t *testing.T) {
		s := newMockStorage()
		original := &nhl.DailySchedule{}

		if err := resource.WriteParsed(context.Background(), s, r, original); err != nil {
			t.Fatalf("WriteParsed() unexpected error: %v", err)
		}

		parsed, err := resource.ReadParsed(context.Background(), s, r)
		if err != nil {
			t.Fatalf("ReadParsed() after WriteParsed() unexpected error: %v", err)
		}
		if parsed == nil {
			t.Fatal("ReadParsed() returned nil after WriteParsed()")
		}
	})
}
