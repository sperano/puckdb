package draftfixtures

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/newsadjust"
)

// Store is an in-memory draftrank.Reader and draftrank.OverrideStore, safe
// for the concurrent field resolvers of a GraphQL test server. Tests set its
// fields directly before serving requests. Latest maps "season/leagueID" to
// the latest snapshot ID.
type Store struct {
	mu         sync.Mutex
	Snapshots  map[uuid.UUID]*draftrank.Snapshot
	Latest     map[string]uuid.UUID
	Refreshes  map[string]*draftrank.Refresh
	Rules      map[string]draft.Snapshot
	Keys       map[string][2]int
	Overrides  []newsadjust.Override
	Changes    int64
	LoadCalls  int
	ResetCalls []string
}

// NewStore returns a store holding the fixture league's rules.
func NewStore() *Store {
	return &Store{
		Snapshots: make(map[uuid.UUID]*draftrank.Snapshot), Latest: make(map[string]uuid.UUID),
		Refreshes: make(map[string]*draftrank.Refresh),
		Rules:     map[string]draft.Snapshot{Key(Season, LeagueID): Rules()},
		Keys:      map[string][2]int{LeagueKey: {Season, LeagueID}},
	}
}

// Key names a league in the store's maps.
func Key(season, leagueID int) string {
	return fmt.Sprintf("%d/%d", season, leagueID)
}

// Add stores a snapshot as its league's latest.
func (s *Store) Add(snapshot *draftrank.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Snapshots[snapshot.ID] = snapshot
	s.Latest[Key(snapshot.League.Season, snapshot.League.LeagueID)] = snapshot.ID
}

func (s *Store) FindLeagueKey(_ context.Context, leagueKey string) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, known := s.Keys[leagueKey]
	if !known {
		return 0, 0, fmt.Errorf("%w: %s", draftrank.ErrUnknownLeague, leagueKey)
	}
	return ids[0], ids[1], nil
}

func (s *Store) LeagueRules(_ context.Context, season, leagueID int) (*draft.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rules, known := s.Rules[Key(season, leagueID)]
	if !known {
		return nil, nil
	}
	return &rules, nil
}

func (s *Store) ListLeagueRules(_ context.Context, season int) ([]draft.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []draft.Snapshot
	for _, rules := range s.Rules {
		if rules.Rules.Season == season {
			out = append(out, rules)
		}
	}
	slices.SortFunc(out, func(a, b draft.Snapshot) int { return a.Rules.LeagueID - b.Rules.LeagueID })
	return out, nil
}

func (s *Store) LatestSnapshotID(_ context.Context, season, leagueID int) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Latest[Key(season, leagueID)], nil
}

func (s *Store) SnapshotInfo(_ context.Context, id uuid.UUID) (*draftrank.SnapshotInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, known := s.Snapshots[id]
	if !known {
		return nil, fmt.Errorf("%w: %s", draftrank.ErrSnapshotNotFound, id)
	}
	info := snapshot.SnapshotInfo
	return &info, nil
}

func (s *Store) LoadSnapshot(_ context.Context, id uuid.UUID) (*draftrank.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LoadCalls++
	snapshot, known := s.Snapshots[id]
	if !known {
		return nil, fmt.Errorf("%w: %s", draftrank.ErrSnapshotNotFound, id)
	}
	return snapshot, nil
}

func (s *Store) LatestRefresh(_ context.Context, season, leagueID int) (*draftrank.Refresh, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Refreshes[Key(season, leagueID)], nil
}

func (s *Store) OverrideChanges(context.Context, string, time.Time, time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Changes, nil
}

func (s *Store) ListOverrides(context.Context) ([]newsadjust.Override, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.Overrides), nil
}

func (s *Store) CreateOverride(_ context.Context, o newsadjust.Override) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Overrides = append(s.Overrides, o)
	return nil
}

func (s *Store) ResetOverride(_ context.Context, id string, at time.Time, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Overrides {
		if s.Overrides[i].ID == id {
			s.Overrides[i].ResetAt, s.Overrides[i].ResetReason = at, reason
			s.ResetCalls = append(s.ResetCalls, id)
			return nil
		}
	}
	return newsadjust.ErrOverrideNotResettable
}
