package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ImportYahooLeagueDataInput contains parameters for importing league-level Yahoo data.
type ImportYahooLeagueDataInput struct {
	Season   int
	LeagueID int
}

// ImportYahooLeagueDataResult contains the results of importing league-level Yahoo data.
type ImportYahooLeagueDataResult struct {
	TransactionsImported int `json:"transactionsImported"`
	DraftPicksImported   int `json:"draftPicksImported"`
	MatchupsImported     int `json:"matchupsImported"`
}

// ImportYahooLeagueData imports cached transactions, draft results, and matchups for a league.
func (a *SeasonsActivities) ImportYahooLeagueData(ctx context.Context, input ImportYahooLeagueDataInput) (ImportYahooLeagueDataResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportYahooLeagueData", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	result := ImportYahooLeagueDataResult{}

	// Import transactions
	txCount, err := a.importYahooTransactions(ctx, input)
	if err != nil {
		return result, fmt.Errorf("import transactions: %w", err)
	}
	result.TransactionsImported = txCount

	// Import draft results
	draftCount, err := a.importYahooDraftResults(ctx, input)
	if err != nil {
		return result, fmt.Errorf("import draft results: %w", err)
	}
	result.DraftPicksImported = draftCount

	// Import matchups
	matchupCount, err := a.importYahooMatchups(ctx, input)
	if err != nil {
		return result, fmt.Errorf("import matchups: %w", err)
	}
	result.MatchupsImported = matchupCount

	logger.Info("Imported Yahoo league data",
		"season", input.Season,
		"leagueID", input.LeagueID,
		"transactions", result.TransactionsImported,
		"draftPicks", result.DraftPicksImported,
		"matchups", result.MatchupsImported)

	return result, nil
}

// importYahooTransactions reads cached transaction data and upserts to the database.
func (a *SeasonsActivities) importYahooTransactions(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	res := resource.Transactions{Season: input.Season, LeagueID: input.LeagueID}
	if !a.Storage.Exists(res.Path()) {
		return 0, nil
	}

	fantasy, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, res)
	if err != nil {
		return 0, fmt.Errorf("read transactions cache: %w", err)
	}

	txns := fantasy.League.Transactions.Slice
	if len(txns) == 0 {
		return 0, nil
	}

	params := make([]sqlcdb.UpsertYahooTransactionBatchParams, len(txns))
	for i, tx := range txns {
		var playersJSON []byte
		if tx.Players.Count > 0 {
			var err error
			playersJSON, err = json.Marshal(tx.Players.Slice)
			if err != nil {
				return 0, fmt.Errorf("marshal transaction players: %w", err)
			}
		}

		params[i] = sqlcdb.UpsertYahooTransactionBatchParams{
			LeagueID:       int32(input.LeagueID),
			TransactionKey: tx.TransactionKey,
			Type:           tx.Type,
			Timestamp:      pgtype.Int8{Int64: int64(tx.Timestamp), Valid: int64(tx.Timestamp) != 0},
			Status:         pgtype.Text{String: tx.Status, Valid: tx.Status != ""},
			Players:        playersJSON,
		}
	}

	var batchErr error
	results := a.ImportQueries.UpsertYahooTransactionBatch(ctx, params)
	results.Exec(func(i int, err error) {
		if err != nil && batchErr == nil {
			batchErr = fmt.Errorf("transaction %s: %w", params[i].TransactionKey, err)
		}
	})
	if batchErr != nil {
		return 0, batchErr
	}
	return len(params), nil
}
