package yahoo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// invalidPlayerPoolErrorType tags pool snapshots whose pages do not belong
// to the league and season of the manifest; retrying cannot fix them.
const invalidPlayerPoolErrorType = "InvalidYahooPlayerPool"

// playerKeySeparator splits "<game_key>.p.<player_id>".
const playerKeySeparator = ".p."

// ImportYahooLeaguePlayersInput names the league whose pool to import.
type ImportYahooLeaguePlayersInput struct {
	Season   int `json:"season"`
	LeagueID int `json:"leagueId"`
}

// ImportYahooLeaguePlayersResult reports the pool import.
type ImportYahooLeaguePlayersResult struct {
	Players int   `json:"players"`
	Removed int64 `json:"removed"`
	// Unavailable is true when no complete, non-empty pool snapshot is cached
	// for a league whose season is not over; the stored pool is then left
	// untouched.
	Unavailable bool `json:"unavailable,omitempty"`
}

// ImportYahooLeaguePlayers replaces the league's stored pool with the latest
// complete snapshot: every player is upserted with the league's eligibility
// and status, then players the snapshot no longer lists are removed. An
// absent or empty snapshot never deletes anything.
func (a *ImportActivities) ImportYahooLeaguePlayers(ctx context.Context, input ImportYahooLeaguePlayersInput) (ImportYahooLeaguePlayersResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooLeaguePlayers")()
	manifest, err := resource.ReadParsed(ctx, a.Storage, resource.LeaguePlayerPool{Season: input.Season, LeagueID: input.LeagueID})
	if errors.Is(err, os.ErrNotExist) {
		return a.missingPool(ctx, input)
	}
	if err != nil {
		return ImportYahooLeaguePlayersResult{}, fmt.Errorf("read player pool manifest %d/%d: %w", input.Season, input.LeagueID, err)
	}
	params, err := a.collectPoolPlayers(ctx, input, manifest)
	if err != nil {
		return ImportYahooLeaguePlayersResult{}, err
	}
	if len(params) == 0 {
		return ImportYahooLeaguePlayersResult{Unavailable: true}, nil
	}
	if err := shared.ExecBatch(a.Queries.UpsertYahooLeaguePlayerBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("league player %s", params[i].PlayerKey)
	}); err != nil {
		return ImportYahooLeaguePlayersResult{}, err
	}
	removed, err := a.Queries.DeleteStaleYahooLeaguePlayers(ctx, sqlcdb.DeleteStaleYahooLeaguePlayersParams{
		LeagueKey: manifest.LeagueKey, FetchedAt: pgtype.Timestamptz{Time: manifest.FetchedAt, Valid: true},
	})
	if err != nil {
		return ImportYahooLeaguePlayersResult{}, fmt.Errorf("remove stale players of league %s: %w", manifest.LeagueKey, err)
	}
	activity.GetLogger(ctx).Info("Imported Yahoo league player pool",
		"season", input.Season, "leagueID", input.LeagueID, "players", len(params), "removed", removed)
	return ImportYahooLeaguePlayersResult{Players: len(params), Removed: removed}, nil
}

// missingPool reports a league without a pool snapshot as unavailable,
// unless its season is over: ended seasons are never downloaded.
func (a *ImportActivities) missingPool(ctx context.Context, input ImportYahooLeaguePlayersInput) (ImportYahooLeaguePlayersResult, error) {
	league, _, err := readLeagueSettings(ctx, a.Storage, resource.League{Season: input.Season, LeagueID: input.LeagueID})
	if err != nil {
		return ImportYahooLeaguePlayersResult{}, err
	}
	return ImportYahooLeaguePlayersResult{Unavailable: !leagueSeasonEnded(league, time.Now())}, nil
}

// collectPoolPlayers reads the manifest's pages, checks every page and
// player belongs to the manifest's league and game, and returns one row per
// Yahoo player (the first page listing a player wins).
func (a *ImportActivities) collectPoolPlayers(ctx context.Context, input ImportYahooLeaguePlayersInput,
	manifest resource.LeaguePlayerPoolManifest) ([]sqlcdb.UpsertYahooLeaguePlayerBatchParams, error) {
	if draft.GameKeyOf(manifest.LeagueKey) != manifest.GameKey {
		return nil, invalidPool(input, fmt.Sprintf("manifest league key %s does not match game key %d", manifest.LeagueKey, manifest.GameKey))
	}
	seen := make(map[int]bool)
	var params []sqlcdb.UpsertYahooLeaguePlayerBatchParams
	for _, start := range manifest.Starts {
		page := resource.LeaguePlayers{
			Season: input.Season, LeagueID: input.LeagueID, DownloadID: manifest.DownloadID, Start: start,
		}
		content, err := resource.ReadParsed(ctx, a.Storage, page)
		if err != nil {
			return nil, fmt.Errorf("read league players %d/%d start %d: %w", input.Season, input.LeagueID, start, err)
		}
		if content.League.Key != manifest.LeagueKey {
			return nil, invalidPool(input, fmt.Sprintf("page %d is for league %s, manifest is for %s", start, content.League.Key, manifest.LeagueKey))
		}
		for _, player := range content.League.Players.Slice {
			if seen[player.ID] {
				continue
			}
			if err := checkPlayerKey(player, manifest.GameKey); err != nil {
				return nil, invalidPool(input, err.Error())
			}
			seen[player.ID] = true
			params = append(params, leaguePlayerParams(input, manifest, player))
		}
	}
	return params, nil
}

// checkPlayerKey makes sure a player's key carries the snapshot's game key,
// so a page from another season can never relabel a player.
func checkPlayerKey(player store.Player, gameKey int) error {
	want := fmt.Sprintf("%d%s%d", gameKey, playerKeySeparator, player.ID)
	if player.ID <= 0 || player.Key != want {
		return fmt.Errorf("player key %q does not match game %d player %d", player.Key, gameKey, player.ID)
	}
	return nil
}

func invalidPool(input ImportYahooLeaguePlayersInput, reason string) error {
	return temporal.NewNonRetryableApplicationError(
		fmt.Sprintf("player pool of season %d league %d is inconsistent: %s", input.Season, input.LeagueID, reason),
		invalidPlayerPoolErrorType, nil)
}

func leaguePlayerParams(input ImportYahooLeaguePlayersInput, manifest resource.LeaguePlayerPoolManifest,
	player store.Player) sqlcdb.UpsertYahooLeaguePlayerBatchParams {
	eligible := player.EligiblePositions
	if eligible == nil {
		eligible = []string{}
	}
	return sqlcdb.UpsertYahooLeaguePlayerBatchParams{
		LeagueKey: manifest.LeagueKey, Season: int32(input.Season), LeagueID: int32(input.LeagueID),
		GameKey: int32(manifest.GameKey), PlayerID: int32(player.ID), PlayerKey: player.Key,
		FullName: strings.TrimSpace(player.Name.Full), EditorialTeamAbbr: player.EditorialTeamAbbr,
		DisplayPosition: player.DisplayPosition, PrimaryPosition: player.PrimaryPosition,
		PositionType: player.PositionType, EligiblePositions: eligible,
		Status: player.Status, StatusFull: player.StatusFull, InjuryNote: player.InjuryNote,
		OnDisabledList: player.OnDisabledList != 0,
		FetchedAt:      pgtype.Timestamptz{Time: manifest.FetchedAt, Valid: true},
	}
}
