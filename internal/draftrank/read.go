package draftrank

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
)

// Read is one league's ranking as every consumer must see it: the snapshot
// served (nil unless Status is READY), the latest refresh attempt, and every
// condition known at read time. Issues are computed now, not stored: a
// snapshot ages, overrides change and rules are re-imported after it was
// built. RulesHash identifies the league's current rules ("" when none are
// imported), so a consumer can tell which rules the issues were checked
// against.
type Read struct {
	League           League
	Status           Status
	Snapshot         *Snapshot
	LatestSnapshotID uuid.UUID
	Refresh          *Refresh
	Issues           []Issue
	RulesHash        string
}

// Read loads the league's latest snapshot, or snapshotID when set, with its
// dynamic issues. The rankings API and the live draft board both read
// through it, so they report the same freshness, override, rule and refresh
// conditions for the same snapshot.
func (s *Service) Read(ctx context.Context, ref LeagueRef, snapshotID uuid.UUID) (Read, error) {
	ref, err := s.resolve(ctx, ref)
	if err != nil {
		return Read{}, err
	}
	latestID, refresh, err := s.latest(ctx, ref)
	if err != nil {
		return Read{}, err
	}
	target := snapshotID
	if target == uuid.Nil {
		target = latestID
	}
	if target == uuid.Nil {
		return s.emptyRead(ctx, ref, refresh)
	}
	snapshot, err := s.load(ctx, target)
	if err != nil {
		return Read{}, err
	}
	if snapshot.League.Season != ref.Season || snapshot.League.LeagueID != ref.LeagueID {
		return Read{}, fmt.Errorf("%w: snapshot %s belongs to league %s", ErrInvalidQuery, target, snapshot.League.LeagueKey)
	}
	checked, err := s.snapshotIssues(ctx, &snapshot.SnapshotInfo, latestID, refresh)
	if err != nil {
		return Read{}, err
	}
	return Read{League: snapshot.League, Status: StatusReady, Snapshot: snapshot, LatestSnapshotID: latestID,
		Refresh: refresh, Issues: checked.issues, RulesHash: checked.rulesHash}, nil
}

// emptyRead answers for a league without a snapshot: why there is none,
// from its rules and its latest refresh attempt.
func (s *Service) emptyRead(ctx context.Context, ref LeagueRef, refresh *Refresh) (Read, error) {
	read := Read{
		League: League{
			Season: ref.Season, LeagueID: ref.LeagueID, LeagueKey: ref.LeagueKey,
			Categories: []Category{}, RosterSlots: []draft.RosterSlot{},
		},
		Status: StatusNotComputed, Refresh: refresh,
	}
	rulesIssues, err := s.rulesIssues(ctx, ref, &read)
	if err != nil {
		return Read{}, err
	}
	var refreshIssues []Issue
	read.Status, refreshIssues = s.refreshStatus(refresh)
	read.Issues = append(rulesIssues, refreshIssues...)
	return read, nil
}
