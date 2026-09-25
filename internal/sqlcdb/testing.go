package sqlcdb

import "github.com/jackc/pgx/v5"

// NewEnsurePlayerExistsBatchBatchResults creates batch results for testing.
func NewEnsurePlayerExistsBatchBatchResults(br pgx.BatchResults, count int) *EnsurePlayerExistsBatchBatchResults {
	return &EnsurePlayerExistsBatchBatchResults{br: br, tot: count}
}

// NewUpsertSeasonRosterBatchBatchResults creates batch results for testing.
func NewUpsertSeasonRosterBatchBatchResults(br pgx.BatchResults, count int) *UpsertSeasonRosterBatchBatchResults {
	return &UpsertSeasonRosterBatchBatchResults{br: br, tot: count}
}

// NewUpsertPlayerAwardBatchBatchResults creates batch results for testing.
func NewUpsertPlayerAwardBatchBatchResults(br pgx.BatchResults, count int) *UpsertPlayerAwardBatchBatchResults {
	return &UpsertPlayerAwardBatchBatchResults{br: br, tot: count}
}

// NewUpsertPlayerSeasonTotalBatchBatchResults creates batch results for testing.
func NewUpsertPlayerSeasonTotalBatchBatchResults(br pgx.BatchResults, count int) *UpsertPlayerSeasonTotalBatchBatchResults {
	return &UpsertPlayerSeasonTotalBatchBatchResults{br: br, tot: count}
}

// NewUpsertGameSkaterStatsBatchBatchResults creates batch results for testing.
func NewUpsertGameSkaterStatsBatchBatchResults(br pgx.BatchResults, count int) *UpsertGameSkaterStatsBatchBatchResults {
	return &UpsertGameSkaterStatsBatchBatchResults{br: br, tot: count}
}

// NewUpsertGameGoalieStatsBatchBatchResults creates batch results for testing.
func NewUpsertGameGoalieStatsBatchBatchResults(br pgx.BatchResults, count int) *UpsertGameGoalieStatsBatchBatchResults {
	return &UpsertGameGoalieStatsBatchBatchResults{br: br, tot: count}
}

// NewUpsertGameBroadcastBatchBatchResults creates batch results for testing.
func NewUpsertGameBroadcastBatchBatchResults(br pgx.BatchResults, count int) *UpsertGameBroadcastBatchBatchResults {
	return &UpsertGameBroadcastBatchBatchResults{br: br, tot: count}
}

// NewUpsertPlayEventBatchBatchResults creates batch results for testing.
func NewUpsertPlayEventBatchBatchResults(br pgx.BatchResults, count int) *UpsertPlayEventBatchBatchResults {
	return &UpsertPlayEventBatchBatchResults{br: br, tot: count}
}

// NewUpsertShiftBatchBatchResults creates batch results for testing.
func NewUpsertShiftBatchBatchResults(br pgx.BatchResults, count int) *UpsertShiftBatchBatchResults {
	return &UpsertShiftBatchBatchResults{br: br, tot: count}
}

// NewUpsertStandingsSnapshotBatchBatchResults creates batch results for testing.
func NewUpsertStandingsSnapshotBatchBatchResults(br pgx.BatchResults, count int) *UpsertStandingsSnapshotBatchBatchResults {
	return &UpsertStandingsSnapshotBatchBatchResults{br: br, tot: count}
}

// NewUpsertClubSkaterStatsBatchBatchResults creates batch results for testing.
func NewUpsertClubSkaterStatsBatchBatchResults(br pgx.BatchResults, count int) *UpsertClubSkaterStatsBatchBatchResults {
	return &UpsertClubSkaterStatsBatchBatchResults{br: br, tot: count}
}

// NewUpsertClubGoalieStatsBatchBatchResults creates batch results for testing.
func NewUpsertClubGoalieStatsBatchBatchResults(br pgx.BatchResults, count int) *UpsertClubGoalieStatsBatchBatchResults {
	return &UpsertClubGoalieStatsBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooLeagueRosterPositionBatchBatchResults creates batch results for testing.
func NewUpsertYahooLeagueRosterPositionBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooLeagueRosterPositionBatchBatchResults {
	return &UpsertYahooLeagueRosterPositionBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooLeagueStatCategoryBatchBatchResults creates batch results for testing.
func NewUpsertYahooLeagueStatCategoryBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooLeagueStatCategoryBatchBatchResults {
	return &UpsertYahooLeagueStatCategoryBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooTeamBatchBatchResults creates batch results for testing.
func NewUpsertYahooTeamBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooTeamBatchBatchResults {
	return &UpsertYahooTeamBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooTeamManagerBatchBatchResults creates batch results for testing.
func NewUpsertYahooTeamManagerBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooTeamManagerBatchBatchResults {
	return &UpsertYahooTeamManagerBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooTeamSummaryBatchBatchResults creates batch results for testing.
func NewUpsertYahooTeamSummaryBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooTeamSummaryBatchBatchResults {
	return &UpsertYahooTeamSummaryBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooTeamRosterBatchBatchResults creates batch results for testing.
func NewUpsertYahooTeamRosterBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooTeamRosterBatchBatchResults {
	return &UpsertYahooTeamRosterBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooTransactionBatchBatchResults creates batch results for testing.
func NewUpsertYahooTransactionBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooTransactionBatchBatchResults {
	return &UpsertYahooTransactionBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooTransactionPlayerBatchBatchResults creates batch results for testing.
func NewUpsertYahooTransactionPlayerBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooTransactionPlayerBatchBatchResults {
	return &UpsertYahooTransactionPlayerBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooDraftResultBatchBatchResults creates batch results for testing.
func NewUpsertYahooDraftResultBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooDraftResultBatchBatchResults {
	return &UpsertYahooDraftResultBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooMatchupBatchBatchResults creates batch results for testing.
func NewUpsertYahooMatchupBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooMatchupBatchBatchResults {
	return &UpsertYahooMatchupBatchBatchResults{br: br, tot: count}
}

// NewUpsertYahooLeaguePlayerBatchBatchResults creates batch results for testing.
func NewUpsertYahooLeaguePlayerBatchBatchResults(br pgx.BatchResults, count int) *UpsertYahooLeaguePlayerBatchBatchResults {
	return &UpsertYahooLeaguePlayerBatchBatchResults{br: br, tot: count}
}
