package cmd

import (
	"context"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
)

// checkYahooSeasonCache checks Yahoo fantasy files (leagues, teams, rosters,
// summaries). Iterates core.AllFileTypes and emits one entry per FileType this
// checker recognizes, in declaration order.
func checkYahooSeasonCache(ctx context.Context, storage store.Storage, nhlSeason simpleSeason, yahooCfg config.Season) []cacheMetrics {
	seasonYear := nhlSeason.startYear
	daysInSeason := core.CountDays(nhlSeason.start, nhlSeason.end)

	leagueCount := countLeagueFiles(ctx, storage, seasonYear, yahooCfg)

	totalTeams := 0
	for _, league := range yahooCfg.Leagues {
		totalTeams += len(league.TeamIDs)
	}
	teamCount := countTeamFiles(ctx, storage, seasonYear, yahooCfg)

	now := time.Now()
	expectedRosters := totalTeams * daysInSeason
	rosterCount := countDailyTeamFiles(ctx, storage, nhlSeason, yahooCfg, now, rosterResource)

	expectedSummaries := totalTeams * daysInSeason
	summaryCount := countDailyTeamFiles(ctx, storage, nhlSeason, yahooCfg, now, teamSummaryResource)

	cacheData := make([]cacheMetrics, 0, 4)
	for _, ft := range core.AllFileTypes {
		var expected, found int
		switch ft {
		case core.League:
			expected, found = len(yahooCfg.Leagues), leagueCount
		case core.Team:
			expected, found = totalTeams, teamCount
		case core.Roster:
			expected, found = expectedRosters, rosterCount
		case core.TeamSummary:
			expected, found = expectedSummaries, summaryCount
		default:
			continue
		}
		cacheData = append(cacheData, cacheMetrics{
			seasonYear: seasonYear,
			fileType:   ft,
			expected:   expected,
			found:      found,
		})
	}
	return cacheData
}

func countLeagueFiles(ctx context.Context, storage store.Storage, seasonYear int, cfg config.Season) int {
	count := 0
	for _, league := range cfg.Leagues {
		if resource.Exists(ctx, storage, resource.League{Season: seasonYear, LeagueID: league.LeagueID}) {
			count++
		}
	}
	return count
}

func countTeamFiles(ctx context.Context, storage store.Storage, seasonYear int, cfg config.Season) int {
	count := 0
	for _, league := range cfg.Leagues {
		for _, teamID := range league.TeamIDs {
			if resource.Exists(ctx, storage, resource.Team{Season: seasonYear, LeagueID: league.LeagueID, TeamID: teamID}) {
				count++
			}
		}
	}
	return count
}

// dailyTeamResource builds the cached file of one Yahoo team on one day.
type dailyTeamResource func(leagueID, teamID int, date time.Time) core.Resource

func rosterResource(leagueID, teamID int, date time.Time) core.Resource {
	return resource.Roster{LeagueID: leagueID, TeamID: teamID, Date: date}
}

func teamSummaryResource(leagueID, teamID int, date time.Time) core.Resource {
	return resource.TeamSummary{LeagueID: leagueID, TeamID: teamID, Date: date}
}

// countDailyTeamFiles counts the files build names for every configured team
// on each day of the season, from its start through its end or now, whichever
// comes first (both inclusive).
func countDailyTeamFiles(ctx context.Context, storage store.Storage, nhlSeason simpleSeason, cfg config.Season, now time.Time, build dailyTeamResource) int {
	end := nhlSeason.end
	if end.After(now) {
		end = now
	}

	count := 0
	for current := nhlSeason.start; !current.After(end); current = current.AddDate(0, 0, 1) {
		for _, league := range cfg.Leagues {
			for _, teamID := range league.TeamIDs {
				if resource.Exists(ctx, storage, build(league.LeagueID, teamID, current)) {
					count++
				}
			}
		}
	}
	return count
}
