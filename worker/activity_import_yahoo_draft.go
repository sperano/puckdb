package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
)

// importYahooDraftResults reads cached draft result data and upserts to the database.
func (a *SeasonsActivities) importYahooDraftResults(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	res := resource.DraftResults{Season: input.Season, LeagueID: input.LeagueID}
	if !a.Storage.Exists(res.Path()) {
		return 0, nil
	}

	fantasy, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, res)
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

	if err := execBatch(a.ImportQueries.UpsertYahooDraftResultBatch(ctx, params), func(i int) string {
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

// Ensure store.DraftResult is used for XML parsing (compile-time documentation).
var _ = store.DraftResult{}
