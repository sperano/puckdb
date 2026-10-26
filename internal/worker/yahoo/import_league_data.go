package yahoo

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportYahooLeagueData imports cached transactions, draft results, and matchups for a league.
func (a *ImportActivities) ImportYahooLeagueData(ctx context.Context, input ImportYahooLeagueDataInput) (ImportYahooLeagueDataResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooLeagueData")()

	logger := activity.GetLogger(ctx)
	result := ImportYahooLeagueDataResult{}
	optionalResources := []struct {
		name string
		res  core.Resource
	}{
		{name: "transactions", res: resource.Transactions{Season: input.Season, LeagueID: input.LeagueID}},
		{name: "draft results", res: resource.DraftResults{Season: input.Season, LeagueID: input.LeagueID}},
		{name: "matchups", res: resource.Matchups{Season: input.Season, LeagueID: input.LeagueID, Week: 1}},
	}
	for _, optional := range optionalResources {
		if !resource.Exists(ctx, a.Storage, optional.res) {
			result.UnavailableResources = append(result.UnavailableResources, optional.name)
		}
	}

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
func (a *ImportActivities) importYahooTransactions(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	res := resource.Transactions{Season: input.Season, LeagueID: input.LeagueID}
	if !resource.Exists(ctx, a.Storage, res) {
		return 0, nil
	}

	fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
	if err != nil {
		return 0, fmt.Errorf("read transactions cache: %w", err)
	}

	txns := fantasy.League.Transactions.Slice
	if len(txns) == 0 {
		return 0, nil
	}

	txParams := make([]sqlcdb.UpsertYahooTransactionBatchParams, len(txns))
	var playerParams []sqlcdb.UpsertYahooTransactionPlayerBatchParams

	for i, tx := range txns {
		leagueID := int32(input.LeagueID)

		txParams[i] = sqlcdb.UpsertYahooTransactionBatchParams{
			LeagueID:       leagueID,
			TransactionKey: tx.TransactionKey,
			Type:           tx.Type,
			Timestamp:      pgtype.Int8{Int64: int64(tx.Timestamp), Valid: int64(tx.Timestamp) != 0},
			Status:         pgtype.Text{String: tx.Status, Valid: tx.Status != ""},
		}

		for _, p := range tx.Players.Slice {
			playerParams = append(playerParams, sqlcdb.UpsertYahooTransactionPlayerBatchParams{
				LeagueID:           leagueID,
				TransactionKey:     tx.TransactionKey,
				PlayerID:           int32(p.ID),
				PlayerKey:          p.Key,
				Type:               p.TransactionData.Type,
				SourceType:         p.TransactionData.SourceType,
				SourceTeamKey:      p.TransactionData.SourceTeamKey,
				DestinationType:    p.TransactionData.DestinationType,
				DestinationTeamKey: p.TransactionData.DestinationTeamKey,
			})
		}
	}

	if err := shared.ExecBatch(a.Queries.UpsertYahooTransactionBatch(ctx, txParams), func(i int) string {
		return fmt.Sprintf("transaction %s", txParams[i].TransactionKey)
	}); err != nil {
		return 0, err
	}

	if len(playerParams) > 0 {
		if err := shared.ExecBatch(a.Queries.UpsertYahooTransactionPlayerBatch(ctx, playerParams), func(i int) string {
			return fmt.Sprintf("transaction player %s player_id=%d", playerParams[i].TransactionKey, playerParams[i].PlayerID)
		}); err != nil {
			return 0, err
		}
	}

	return len(txParams), nil
}

// importYahooDraftResults reads cached draft result data and upserts to the database.
func (a *ImportActivities) importYahooDraftResults(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	res := resource.DraftResults{Season: input.Season, LeagueID: input.LeagueID}
	if !resource.Exists(ctx, a.Storage, res) {
		return 0, nil
	}

	fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
	if err != nil {
		return 0, fmt.Errorf("read draft results cache: %w", err)
	}

	picks := fantasy.League.DraftResults.Slice
	if len(picks) == 0 {
		return 0, nil
	}

	params := make([]sqlcdb.UpsertYahooDraftResultBatchParams, 0, len(picks))
	for _, pick := range picks {
		teamID, err := parseYahooTeamKey(pick.TeamKey)
		if err != nil {
			log.Warn().Err(err).Str("team_key", pick.TeamKey).Msg("skipping draft pick with unparseable team key")
			continue
		}
		playerID, err := parseYahooPlayerKey(pick.PlayerKey)
		if err != nil {
			log.Warn().Err(err).Str("player_key", pick.PlayerKey).Msg("skipping draft pick with unparseable player key")
			continue
		}

		var cost pgtype.Int4
		if pick.Cost > 0 {
			cost = pgtype.Int4{Int32: int32(pick.Cost), Valid: true}
		}

		params = append(params, sqlcdb.UpsertYahooDraftResultBatchParams{
			LeagueID: int32(input.LeagueID),
			Round:    int32(pick.Round),
			Pick:     int32(pick.Pick),
			TeamID:   int32(teamID),
			PlayerID: int32(playerID),
			Cost:     cost,
		})
	}

	if len(params) == 0 {
		return 0, nil
	}

	if err := shared.ExecBatch(a.Queries.UpsertYahooDraftResultBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("draft pick round %d pick %d", params[i].Round, params[i].Pick)
	}); err != nil {
		return 0, err
	}
	return len(params), nil
}

// parseYahooTeamKey extracts the numeric team ID from a Yahoo team key.
// Format: "gamekey.l.leagueid.t.teamid" (e.g., "423.l.12345.t.1" → 1)
func parseYahooTeamKey(key string) (int, error) {
	parts := strings.Split(key, ".t.")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid team key: %s", key)
	}
	return strconv.Atoi(parts[1])
}

// parseYahooPlayerKey extracts the numeric player ID from a Yahoo player key.
// Format: "gamekey.p.playerid" (e.g., "423.p.6616" → 6616)
func parseYahooPlayerKey(key string) (int, error) {
	parts := strings.Split(key, ".p.")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid player key: %s", key)
	}
	return strconv.Atoi(parts[1])
}

// importYahooMatchups reads cached matchup data for all weeks and upserts to the database.
func (a *ImportActivities) importYahooMatchups(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	var totalImported int

	// Iterate every possible week up to MaxMatchupWeeks, mirroring the fetcher's
	// continue-on-skip semantics (FetchYahooMatchupWeek). The cache may be
	// non-contiguous — an individual week can be absent (e.g. a download that
	// failed mid-sequence) while later weeks are present — so skip absent weeks
	// rather than stopping at the first gap, which would silently under-import.
	for week := 1; week <= MaxMatchupWeeks; week++ {
		res := resource.Matchups{Season: input.Season, LeagueID: input.LeagueID, Week: week}
		if !resource.Exists(ctx, a.Storage, res) {
			continue // Week not cached; later weeks may still be present
		}

		fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
		if err != nil {
			return totalImported, fmt.Errorf("read matchups week %d cache: %w", week, err)
		}

		matchups := fantasy.League.Scoreboard.Matchups.Slice
		if len(matchups) == 0 {
			continue
		}

		params := make([]sqlcdb.UpsertYahooMatchupBatchParams, 0, len(matchups))
		for _, m := range matchups {
			if len(m.Teams.Slice) != 2 {
				continue // Skip malformed matchups
			}

			team1 := m.Teams.Slice[0]
			team2 := m.Teams.Slice[1]

			params = append(params, sqlcdb.UpsertYahooMatchupBatchParams{
				LeagueID:      int32(input.LeagueID),
				Week:          int32(m.Week),
				Team1ID:       int32(team1.TeamID),
				Team2ID:       int32(team2.TeamID),
				Team1Points:   pgtype.Float4{Float32: float32(team1.TeamPoints.Total), Valid: true},
				Team2Points:   pgtype.Float4{Float32: float32(team2.TeamPoints.Total), Valid: true},
				Status:        pgtype.Text{String: m.Status, Valid: m.Status != ""},
				IsPlayoffs:    m.IsPlayoffs != 0,
				IsConsolation: m.IsConsolation != 0,
			})
		}

		if len(params) == 0 {
			continue
		}

		if err := shared.ExecBatch(a.Queries.UpsertYahooMatchupBatch(ctx, params), func(i int) string {
			return fmt.Sprintf("matchup week %d team1 %d vs team2 %d", params[i].Week, params[i].Team1ID, params[i].Team2ID)
		}); err != nil {
			return totalImported, err
		}
		totalImported += len(params)
	}

	return totalImported, nil
}

// Ensure store.DraftResult is used for XML parsing (compile-time documentation).
var _ = store.DraftResult{}
