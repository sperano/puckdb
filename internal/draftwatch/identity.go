// Package draftwatch connects Yahoo's mutable draft resources to the durable
// draft-session reducer and PostgreSQL repository.
package draftwatch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/yahookey"
)

// ErrLeagueNotImported means no imported league-rule snapshot matches the
// requested league (and season), so its identity cannot be resolved.
var ErrLeagueNotImported = errors.New("no imported rule snapshot")

// Identity is the season-safe identity of one Yahoo league.
type Identity struct {
	LeagueKey string
	Season    int
	LeagueID  int
	GameKey   int
}

// NewestSeason, as the season of ResolveIdentity, resolves a numeric league
// ID to the season whose rules were seen last (draft.AnySeason): ordered by
// last_seen_at, so re-importing an older season's rules makes it the newest.
const NewestSeason = draft.AnySeason

// ResolveIdentity resolves a full league key or a numeric ID from imported
// league-rule metadata. A full key names its season itself; a season other
// than NewestSeason must then be the key's, or the league is not found. A
// numeric ID with NewestSeason intentionally chooses the most recently seen
// season of that ID (draftrank.ParseLeague instead requires one): the live
// draft is the newest season's, and a CLI caller names --draft-season when
// the ID exists in several seasons. Keys and IDs are parsed by
// draft.ParseLeagueKey and draft.ParseLeagueID, as in draftrank.
func ResolveIdentity(ctx context.Context, pool *pgxpool.Pool, ref string, season int) (Identity, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Identity{}, errors.New("league is required")
	}
	if season < NewestSeason {
		return Identity{}, fmt.Errorf("season %d is not a season start year", season)
	}
	if season > math.MaxInt32 {
		return Identity{}, fmt.Errorf("season %d is above %d, the largest season the database stores (PostgreSQL integer)",
			season, math.MaxInt32)
	}
	queries := sqlcdb.New(pool)
	if draft.IsLeagueKey(ref) {
		gameKey, leagueID, err := draft.ParseLeagueKey(ref)
		if err != nil {
			return Identity{}, err
		}
		resolvedSeason, err := findSeasonForKey(ctx, queries, ref, season)
		if err != nil {
			return Identity{}, err
		}
		return Identity{LeagueKey: ref, Season: resolvedSeason, LeagueID: leagueID, GameKey: gameKey}, nil
	}
	leagueID, err := draft.ParseLeagueID(ref)
	if err != nil {
		return Identity{}, err
	}
	return findIdentityForID(ctx, queries, leagueID, season)
}

// ParseTeamKey validates that key belongs to id and returns its numeric team.
func ParseTeamKey(key string, id Identity) (int, error) {
	parsed, err := yahookey.ParseTeamKey(key)
	if err != nil {
		return 0, fmt.Errorf("invalid Yahoo team key %q", key)
	}
	if parsed.League.GameKey != id.GameKey || parsed.League.LeagueID != id.LeagueID {
		return 0, fmt.Errorf("yahoo team key %q does not belong to league %s", key, id.LeagueKey)
	}
	return parsed.TeamID, nil
}

// ParsePlayerKey validates that key belongs to the league's game.
func ParsePlayerKey(key string, id Identity) (int, error) {
	parsed, err := yahookey.ParsePlayerKey(key)
	if err != nil {
		return 0, fmt.Errorf("invalid Yahoo player key %q", key)
	}
	if parsed.GameKey != id.GameKey {
		return 0, fmt.Errorf("yahoo player key %q does not belong to game %d", key, id.GameKey)
	}
	return parsed.PlayerID, nil
}

func findSeasonForKey(ctx context.Context, queries *sqlcdb.Queries, key string, requested int) (int, error) {
	row, err := queries.FindYahooLeagueRuleSnapshotByKey(ctx, sqlcdb.FindYahooLeagueRuleSnapshotByKeyParams{
		LeagueKey: key, Season: int32(requested),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, keyNotImportedError(ctx, queries, key, requested)
		}
		return 0, fmt.Errorf("resolve league %s: %w", key, err)
	}
	return int(row.Season), nil
}

// keyNotImportedError explains why a full key has no rules in the requested
// season: a key imported for another season names that season, since syncing
// again would not help.
func keyNotImportedError(ctx context.Context, queries *sqlcdb.Queries, key string, requested int) error {
	if requested != NewestSeason {
		row, err := queries.FindYahooLeagueRuleSnapshotByKey(ctx, sqlcdb.FindYahooLeagueRuleSnapshotByKeyParams{
			LeagueKey: key, Season: NewestSeason,
		})
		if err == nil {
			return fmt.Errorf("league %s%s: %w; the key belongs to season %d", key, seasonLabel(requested),
				ErrLeagueNotImported, row.Season)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("resolve league %s: %w", key, err)
		}
	}
	return fmt.Errorf("league %s%s: %w; sync its league metadata first", key, seasonLabel(requested), ErrLeagueNotImported)
}

func findIdentityForID(ctx context.Context, queries *sqlcdb.Queries, leagueID, requestedSeason int) (Identity, error) {
	row, err := queries.FindYahooLeagueRuleSnapshotByID(ctx, sqlcdb.FindYahooLeagueRuleSnapshotByIDParams{
		LeagueID: int32(leagueID), Season: int32(requestedSeason),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Identity{}, fmt.Errorf("league ID %d%s: %w; sync its league metadata first",
				leagueID, seasonLabel(requestedSeason), ErrLeagueNotImported)
		}
		return Identity{}, fmt.Errorf("resolve league ID %d: %w", leagueID, err)
	}
	gameKey, parsedID, err := draft.ParseLeagueKey(row.LeagueKey)
	if err != nil || parsedID != leagueID {
		return Identity{}, fmt.Errorf("stored league key %q is inconsistent with league ID %d", row.LeagueKey, leagueID)
	}
	return Identity{LeagueKey: row.LeagueKey, Season: int(row.Season), LeagueID: leagueID, GameKey: gameKey}, nil
}

// seasonLabel names the season a lookup was restricted to, if any.
func seasonLabel(season int) string {
	if season == NewestSeason {
		return " in any season"
	}
	return fmt.Sprintf(" season %d", season)
}
