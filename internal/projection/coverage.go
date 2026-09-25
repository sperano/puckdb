package projection

import (
	"fmt"
	"slices"
	"strings"
)

type CoverageIssueCode string

const (
	CoverageUnsupportedCategory CoverageIssueCode = "unsupported_category"
	CoverageMissingProjection   CoverageIssueCode = "missing_projection"
	CoverageMissingPlayerPool   CoverageIssueCode = "missing_player_pool"
)

type CoverageIssue struct {
	LeagueID    int
	StatID      int
	Category    string
	PlayerKey   string
	Code        CoverageIssueCode
	Explanation string
}

type CoverageError struct {
	Issues []CoverageIssue
}

func (e *CoverageError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		location := fmt.Sprintf("league %d category %d (%s)", issue.LeagueID, issue.StatID, issue.Category)
		if issue.PlayerKey != "" {
			location += " player " + issue.PlayerKey
		}
		parts = append(parts, location+": "+issue.Explanation)
	}
	return "projection coverage failed: " + strings.Join(parts, "; ")
}

// ValidateCoverage fails with a deterministic issue for every enabled league
// category that is unsupported or absent from a draftable player projection.
// Historical players outside pool are intentionally ignored.
func ValidateCoverage(snapshot Snapshot, categories []LeagueCategory, pool []PoolPlayer) error {
	issues := make([]CoverageIssue, 0)
	if hasScoringCategory(categories) && len(pool) == 0 {
		issues = append(issues, CoverageIssue{
			Code: CoverageMissingPlayerPool, Explanation: "draftable player pool is required",
		})
	}
	projections := make(map[string]PlayerProjection, len(snapshot.Players))
	for _, player := range snapshot.Players {
		projections[player.PlayerKey] = player
	}
	for _, category := range categories {
		if category.DisplayOnly {
			continue
		}
		kind, stat, supported := yahooCategoryStat(category.StatID)
		if !supported {
			issues = append(issues, CoverageIssue{
				LeagueID: category.LeagueID, StatID: category.StatID, Category: category.Name,
				Code: CoverageUnsupportedCategory, Explanation: "unsupported Yahoo scoring category",
			})
			continue
		}
		relevantPlayers := 0
		for _, candidate := range pool {
			if candidate.Kind != kind {
				continue
			}
			relevantPlayers++
			player, exists := projections[candidate.PlayerKey]
			if !exists {
				issues = append(issues, CoverageIssue{
					LeagueID: category.LeagueID, StatID: category.StatID, Category: category.Name,
					PlayerKey: candidate.PlayerKey, Code: CoverageMissingProjection,
					Explanation: "player is absent from projection snapshot",
				})
				continue
			}
			if _, exists := player.Values[stat]; exists {
				continue
			}
			issues = append(issues, CoverageIssue{
				LeagueID: category.LeagueID, StatID: category.StatID, Category: category.Name,
				PlayerKey: player.PlayerKey, Code: CoverageMissingProjection,
				Explanation: fmt.Sprintf("missing %s projection", stat),
			})
		}
		if relevantPlayers == 0 {
			issues = append(issues, CoverageIssue{
				LeagueID: category.LeagueID, StatID: category.StatID, Category: category.Name,
				Code:        CoverageMissingProjection,
				Explanation: fmt.Sprintf("draftable pool has no %s players", kind),
			})
		}
	}
	if len(issues) == 0 {
		return nil
	}
	slices.SortFunc(issues, compareCoverageIssues)
	return &CoverageError{Issues: issues}
}

func hasScoringCategory(categories []LeagueCategory) bool {
	return slices.ContainsFunc(categories, func(category LeagueCategory) bool {
		return !category.DisplayOnly
	})
}

func compareCoverageIssues(a, b CoverageIssue) int {
	if order := a.LeagueID - b.LeagueID; order != 0 {
		return order
	}
	if order := a.StatID - b.StatID; order != 0 {
		return order
	}
	if order := strings.Compare(a.PlayerKey, b.PlayerKey); order != 0 {
		return order
	}
	return strings.Compare(string(a.Code), string(b.Code))
}

func yahooCategoryStat(statID int) (PlayerKind, Stat, bool) {
	switch statID {
	case 1:
		return PlayerKindSkater, StatGoals, true
	case 2:
		return PlayerKindSkater, StatAssists, true
	case 3:
		return PlayerKindSkater, StatPoints, true
	case 4:
		return PlayerKindSkater, StatPlusMinus, true
	case 5:
		return PlayerKindSkater, StatPenaltyMinutes, true
	case 8:
		return PlayerKindSkater, StatPowerPlayPoints, true
	case 14:
		return PlayerKindSkater, StatShotsOnGoal, true
	case 19:
		return PlayerKindGoalie, StatWins, true
	case 22:
		return PlayerKindGoalie, StatGoalsAgainst, true
	case 23:
		return PlayerKindGoalie, StatGoalsAgainstAvg, true
	case 24:
		return PlayerKindGoalie, StatShotsAgainst, true
	case 25:
		return PlayerKindGoalie, StatSaves, true
	case 26:
		return PlayerKindGoalie, StatSavePercentage, true
	case 27:
		return PlayerKindGoalie, StatShutouts, true
	case 31:
		return PlayerKindSkater, StatHits, true
	case 32:
		return PlayerKindSkater, StatBlockedShots, true
	default:
		return "", "", false
	}
}

// YahooCategoryStat maps a supported Yahoo scoring ID to the projected stat
// and player kind. Ranking and coverage use the same versioned mapping.
func YahooCategoryStat(statID int) (PlayerKind, Stat, bool) {
	return yahooCategoryStat(statID)
}
