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
