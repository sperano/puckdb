package draftrank

import (
	"cmp"
	"context"
	"slices"

	"github.com/google/uuid"
)

// LeagueSummary is one league's rules and ranking state, without rows.
type LeagueSummary struct {
	League   League        `json:"league"`
	Status   Status        `json:"status"`
	Snapshot *SnapshotInfo `json:"snapshot,omitempty"`
	Refresh  *Refresh      `json:"refresh,omitempty"`
	Issues   []Issue       `json:"issues"`
}

// Leagues summarizes every league of the season with imported rules, plus
// each of the configured league IDs (which may have none yet), by ID.
func (s *Service) Leagues(ctx context.Context, season int, configured []int) ([]LeagueSummary, error) {
	rules, err := s.reader.ListLeagueRules(ctx, season)
	if err != nil {
		return nil, err
	}
	ids := slices.Clone(configured)
	for _, r := range rules {
		ids = append(ids, r.Rules.LeagueID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	summaries := make([]LeagueSummary, 0, len(ids))
	for _, id := range ids {
		summary, err := s.League(ctx, LeagueRef{Season: season, LeagueID: id})
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	slices.SortFunc(summaries, func(a, b LeagueSummary) int { return cmp.Compare(a.League.LeagueID, b.League.LeagueID) })
	return summaries, nil
}

// League summarizes one league: its latest snapshot (without players) or
// why there is none.
func (s *Service) League(ctx context.Context, ref LeagueRef) (LeagueSummary, error) {
	ref, err := s.resolve(ctx, ref)
	if err != nil {
		return LeagueSummary{}, err
	}
	latestID, refresh, err := s.latest(ctx, ref)
	if err != nil {
		return LeagueSummary{}, err
	}
	if latestID == uuid.Nil {
		page, err := s.emptyPage(ctx, ref, refresh, Query{})
		if err != nil {
			return LeagueSummary{}, err
		}
		return LeagueSummary{League: page.League, Status: page.Status, Refresh: refresh, Issues: page.Issues}, nil
	}
	info, err := s.reader.SnapshotInfo(ctx, latestID)
	if err != nil {
		return LeagueSummary{}, err
	}
	issues, err := s.snapshotIssues(ctx, info, latestID, refresh)
	if err != nil {
		return LeagueSummary{}, err
	}
	return LeagueSummary{League: info.League, Status: StatusReady, Snapshot: info, Refresh: refresh, Issues: issues}, nil
}
