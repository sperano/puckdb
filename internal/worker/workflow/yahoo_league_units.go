package workflow

import (
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/yahoo"
)

// A Yahoo season sync shows one progress bar per configured league. In fetch
// mode a bar advances once per Yahoo download (one activity each); in import
// mode once per workflow-level import step. Totals start as estimates derived
// from the configuration alone, so the parent and the child agree before any
// activity runs, and the child corrects them as activity results arrive.
const (
	// yahooLeagueMetadataUnits is the league settings download (FetchLeague,
	// or the stand-in source's), or the league import.
	yahooLeagueMetadataUnits = 1
	// yahooTeamUnits is one team page download per configured team.
	yahooTeamUnits = 1
	// yahooLeagueResourceUnits is the transactions and draft results downloads.
	yahooLeagueResourceUnits = 2
	// yahooMatchupEndProbeUnits is the request past the league's last week,
	// which Yahoo rejects and which ends the matchup loop.
	yahooMatchupEndProbeUnits = 1
	// yahooPoolPlanUnits and yahooPoolCommitUnits bracket a pool download's pages.
	yahooPoolPlanUnits   = 1
	yahooPoolCommitUnits = 1

	// defaultYahooMatchupEndWeek estimates a league's last matchup week until
	// its settings say otherwise (NHL fantasy seasons run 23-26 weeks).
	defaultYahooMatchupEndWeek = 25
	// defaultYahooPoolPages estimates a pool download with no previous
	// snapshot to size it from (pools run about 1,000-1,500 players).
	defaultYahooPoolPages = 50

	// yahooImportTeamsUnits is a league's share of the batched teams import.
	yahooImportTeamsUnits = 1
	// yahooImportLeagueDataUnits and yahooImportPoolUnits are the league data
	// and player pool imports.
	yahooImportLeagueDataUnits = 1
	yahooImportPoolUnits       = 1
)

// yahooLeagueUnits is the initial Total of a league's bar.
func yahooLeagueUnits(league config.League, mode seasonSyncMode) int {
	if league.UsesTemporaryMetadata() {
		return yahooLeagueMetadataUnits
	}
	if mode == seasonSyncImport {
		units := yahooLeagueMetadataUnits + yahooImportLeagueDataUnits + yahooImportPoolUnits
		if len(league.TeamIDs) > 0 {
			units += yahooImportTeamsUnits
		}
		return units
	}
	return yahooLeagueMetadataUnits + len(league.TeamIDs)*yahooTeamUnits + yahooLeagueResourceUnits +
		yahooMatchupCalls(0) + yahooPoolUnits(true, yahooPoolPages(0))
}

// yahooMatchupCalls is the number of matchup week requests for a league whose
// last week is endWeek (0 when unknown, which uses the default estimate):
// every week plus the rejected probe past the end, capped at the loop bound.
func yahooMatchupCalls(endWeek int) int {
	if endWeek <= 0 {
		endWeek = defaultYahooMatchupEndWeek
	}
	return min(endWeek+yahooMatchupEndProbeUnits, yahoo.MaxMatchupWeeks)
}

// yahooPoolPages estimates a pool download's page count from the previous
// snapshot's player count (0 when there is none, which uses the default).
// The loop stops at the first short page, so a pool of n players takes
// n/pageSize+1 pages: an exact multiple ends on an empty page.
func yahooPoolPages(previousPlayers int) int {
	if previousPlayers <= 0 {
		return defaultYahooPoolPages
	}
	return min(previousPlayers/resource.LeaguePlayersPageSize+1, yahoo.MaxLeaguePlayerPoolPages)
}

// yahooPoolUnits is a league's player pool share of its bar: the plan, plus
// the pages and the commit when a download is due.
func yahooPoolUnits(refresh bool, pages int) int {
	if !refresh {
		return yahooPoolPlanUnits
	}
	return yahooPoolPlanUnits + pages + yahooPoolCommitUnits
}
