package resource_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/metricsfixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
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
		var parseErr *resource.ParseError
		if !errors.As(err, &parseErr) {
			t.Fatalf("ReadParsed() error = %v, want *resource.ParseError", err)
		}
		if err.Error() != parseErr.Err.Error() {
			t.Errorf("ParseError must not alter the message: got %q, want %q", err.Error(), parseErr.Err.Error())
		}
		if errors.Is(err, os.ErrNotExist) {
			t.Error("a parse error must not look like a missing file")
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

// labelTestResources has one resource of every kind the workers store, with
// the paths they write today, grouped by source.
func labelTestResources() []core.Resource {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	const (
		gameID       = nhl.GameID(2024020123)
		playerID     = nhl.PlayerID(8478402)
		gameType     = nhl.GameType(2)
		teamID       = nhl.TeamID(8)
		startSeason  = 2024
		leagueID     = 12345
		yahooTeamID  = 3
		yahooPlayer  = resource.YahooPlayerID(6743)
		teamAbbrev   = "MTL"
		regularGames = 2
	)
	season := nhl.NewSeason(startSeason)
	nhlResources := []core.Resource{
		resource.DailySchedule{Date: date},
		resource.Boxscore{Date: date, GameID: gameID},
		resource.PlayByPlay{Date: date, GameID: gameID},
		resource.ShiftChart{Date: date, GameID: gameID},
		resource.GameStory{Date: date, GameID: gameID},
		resource.SeasonSeries{Date: date, GameID: gameID},
		resource.PlayerLanding{PlayerID: playerID},
		resource.MissingPlayerLanding{PlayerID: playerID},
		resource.Franchises{},
		resource.SeasonsManifest{},
		resource.SeasonStandings{Season: season},
		resource.PlayerGameLog{PlayerID: playerID, Season: season, GameType: regularGames},
		resource.DailyStandings{Date: date},
		resource.ClubStatsResource{Season: startSeason, TeamAbbrev: teamAbbrev, GameType: regularGames},
		resource.ClubScheduleSeason{Season: startSeason, TeamAbbrev: teamAbbrev},
		resource.SeasonRoster{Season: startSeason, TeamAbbrev: teamAbbrev},
	}
	yahooResources := []core.Resource{
		resource.League{Season: startSeason, LeagueID: leagueID},
		resource.Team{Season: startSeason, LeagueID: leagueID, TeamID: yahooTeamID},
		resource.Roster{LeagueID: leagueID, TeamID: yahooTeamID, Date: date},
		resource.TeamSummary{LeagueID: leagueID, TeamID: yahooTeamID, Date: date},
		resource.YahooPlayer{PlayerID: yahooPlayer},
		resource.MissingYahooPlayer{PlayerID: yahooPlayer},
		resource.GameKey{Season: startSeason},
		resource.Transactions{Season: startSeason, LeagueID: leagueID},
		resource.DraftResults{Season: startSeason, LeagueID: leagueID},
		resource.Matchups{Season: startSeason, LeagueID: leagueID, Week: 1},
		resource.LeaguePlayers{Season: startSeason, LeagueID: leagueID, DownloadID: 1},
		resource.LeaguePlayerPool{Season: startSeason, LeagueID: leagueID},
	}
	edgeResources := []core.Resource{
		resource.EdgeSkaterDetail{PlayerID: playerID, Season: season, GameType: gameType},
		resource.EdgeSkaterSpeedDetail{PlayerID: playerID, Season: season, GameType: gameType},
		resource.EdgeSkaterDistanceDetail{PlayerID: playerID, Season: season, GameType: gameType},
		resource.EdgeSkaterShotSpeedDetail{PlayerID: playerID, Season: season, GameType: gameType},
		resource.EdgeSkaterShotLocationDetail{PlayerID: playerID, Season: season, GameType: gameType},
		resource.EdgeSkaterZoneTime{PlayerID: playerID, Season: season, GameType: gameType},
		resource.EdgeSkaterComparison{PlayerID: playerID, Season: season, GameType: gameType},
		resource.EdgeGoalieDetail{GoalieID: playerID, Season: season, GameType: gameType},
		resource.EdgeGoalie5v5Detail{GoalieID: playerID, Season: season, GameType: gameType},
		resource.EdgeGoalieShotLocationDetail{GoalieID: playerID, Season: season, GameType: gameType},
		resource.EdgeGoalieSavePctgDetail{GoalieID: playerID, Season: season, GameType: gameType},
		resource.EdgeGoalieComparison{GoalieID: playerID, Season: season, GameType: gameType},
		resource.EdgeTeamDetail{TeamID: teamID, Season: season, GameType: gameType},
		resource.EdgeTeamSpeedDetail{TeamID: teamID, Season: season, GameType: gameType},
		resource.EdgeTeamDistanceDetail{TeamID: teamID, Season: season, GameType: gameType},
		resource.EdgeTeamShotSpeedDetail{TeamID: teamID, Season: season, GameType: gameType},
		resource.EdgeTeamShotLocationDetail{TeamID: teamID, Season: season, GameType: gameType},
		resource.EdgeTeamZoneTimeDetails{TeamID: teamID, Season: season, GameType: gameType},
		resource.EdgeTeamComparison{TeamID: teamID, Season: season, GameType: gameType},
		resource.EdgeSkaterLanding{Season: season, GameType: gameType},
		resource.EdgeGoalieLanding{Season: season, GameType: gameType},
		resource.EdgeTeamLanding{Season: season, GameType: gameType},
	}
	return append(append(nhlResources, yahooResources...), edgeResources...)
}

// fsOps are the operation labels the resource helpers record.
var fsOps = []string{"write", "exists", "stat", "read", "delete"}

// TestResourceHelpers_LabelMetricsWithResourceType runs every helper on
// every resource through the instrumented storage the workers use and checks
// each operation lands under the resource's own file type, never Unknown.
// Not parallel: it reads the process-wide metrics registry.
func TestResourceHelpers_LabelMetricsWithResourceType(t *testing.T) {
	ctx := context.Background()
	s := store.NewInstrumentedStorage(store.NewMemStorage())
	unknownBefore := fsOpCounts(t, core.Unknown)

	for _, r := range labelTestResources() {
		t.Run(r.Path(), func(t *testing.T) {
			ft := r.Type()
			if ft == core.Unknown {
				t.Fatalf("%T.Type() is Unknown", r)
			}
			before := fsOpCounts(t, ft)

			if err := resource.Write(ctx, s, r, []byte("data")); err != nil {
				t.Fatalf("Write() error: %v", err)
			}
			if !resource.Exists(ctx, s, r) {
				t.Fatal("Exists() = false after Write()")
			}
			if _, err := resource.Stat(ctx, s, r); err != nil {
				t.Fatalf("Stat() error: %v", err)
			}
			if _, err := resource.Read(ctx, s, r); err != nil {
				t.Fatalf("Read() error: %v", err)
			}
			if err := resource.Delete(ctx, s, r); err != nil {
				t.Fatalf("Delete() error: %v", err)
			}

			after := fsOpCounts(t, ft)
			for _, op := range fsOps {
				if got := after[op] - before[op]; got != 1 {
					t.Errorf("%s recorded %d times under file_type %q, want 1", op, got, ft)
				}
			}
		})
	}

	unknownAfter := fsOpCounts(t, core.Unknown)
	for _, op := range fsOps {
		if unknownAfter[op] != unknownBefore[op] {
			t.Errorf("%s recorded %d operations as Unknown", op, unknownAfter[op]-unknownBefore[op])
		}
	}
}

// TestParsedHelpers_LabelMetricsWithResourceType checks ReadParsed and
// WriteParsed, the paths the gob cache uses, label their operations too.
// Not parallel: it reads the process-wide metrics registry.
func TestParsedHelpers_LabelMetricsWithResourceType(t *testing.T) {
	ctx := context.Background()
	s := store.NewInstrumentedStorage(store.NewMemStorage())
	r := resource.DailySchedule{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)}
	before := fsOpCounts(t, core.DailySchedule)

	if err := resource.WriteParsed(ctx, s, r, &nhl.DailySchedule{}); err != nil {
		t.Fatalf("WriteParsed() error: %v", err)
	}
	if _, err := resource.ReadParsed(ctx, s, r); err != nil {
		t.Fatalf("ReadParsed() error: %v", err)
	}

	after := fsOpCounts(t, core.DailySchedule)
	for _, op := range []string{"write", "read"} {
		if got := after[op] - before[op]; got != 1 {
			t.Errorf("%s recorded %d times under DailySchedule, want 1", op, got)
		}
	}
}

// unclassifiedResource is a resource whose type was never declared.
type unclassifiedResource struct{}

func (unclassifiedResource) Path() string        { return "unclassified/file.json" }
func (unclassifiedResource) Type() core.FileType { return core.Unknown }

// TestResourceHelpers_UnclassifiedResourceIsCountedAsUnknown pins the
// outcome for a resource without a file type: the operation still succeeds
// and is counted under the explicit Unknown label rather than dropped.
// Not parallel: it reads the process-wide metrics registry.
func TestResourceHelpers_UnclassifiedResourceIsCountedAsUnknown(t *testing.T) {
	ctx := context.Background()
	s := store.NewInstrumentedStorage(store.NewMemStorage())
	before := fsOpCounts(t, core.Unknown)

	if err := resource.Write(ctx, s, unclassifiedResource{}, []byte("data")); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	if got := fsOpCounts(t, core.Unknown)["write"] - before["write"]; got != 1 {
		t.Errorf("write recorded %d times under Unknown, want 1", got)
	}
}

// fsOpCounts returns the recorded operation counts per operation for ft.
func fsOpCounts(t *testing.T, ft core.FileType) map[string]uint64 {
	t.Helper()
	counts := make(map[string]uint64, len(fsOps))
	for _, op := range fsOps {
		counts[op] = metricsfixtures.FSOpCount(t, op, ft)
	}
	return counts
}
