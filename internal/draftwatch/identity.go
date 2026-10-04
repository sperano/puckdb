// Package draftwatch connects Yahoo's mutable draft resources to the durable
// draft-session reducer and PostgreSQL repository.
package draftwatch

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	leagueKeyParts = 3
	teamKeyParts   = 5
	playerKeyParts = 3
)

// Identity is the season-safe identity of one Yahoo league.
type Identity struct {
	LeagueKey string
	Season    int
	LeagueID  int
	GameKey   int
}

// ResolveIdentity resolves a full league key or a numeric ID from imported
// league-rule metadata. A numeric ID without a season intentionally chooses
// the most recently observed season.
func ResolveIdentity(ctx context.Context, pool *pgxpool.Pool, ref string, season int) (Identity, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Identity{}, fmt.Errorf("league is required")
	}
	if strings.Contains(ref, ".") {
		gameKey, leagueID, err := ParseLeagueKey(ref)
		if err != nil {
			return Identity{}, err
		}
		resolvedSeason, err := findSeasonForKey(ctx, pool, ref, season)
		if err != nil {
			return Identity{}, err
		}
		return Identity{LeagueKey: ref, Season: resolvedSeason, LeagueID: leagueID, GameKey: gameKey}, nil
	}
	leagueID, err := strconv.Atoi(ref)
	if err != nil || leagueID <= 0 {
		return Identity{}, fmt.Errorf("league %q is neither a full Yahoo league key nor a positive numeric ID", ref)
	}
	return findIdentityForID(ctx, pool, leagueID, season)
}

// ParseLeagueKey parses <game-key>.l.<league-id>.
func ParseLeagueKey(key string) (int, int, error) {
	parts := strings.Split(key, ".")
	if len(parts) != leagueKeyParts || parts[1] != "l" {
		return 0, 0, fmt.Errorf("invalid Yahoo league key %q", key)
	}
	gameKey, gameErr := positiveInt(parts[0])
	leagueID, leagueErr := positiveInt(parts[2])
	if gameErr != nil || leagueErr != nil {
		return 0, 0, fmt.Errorf("invalid Yahoo league key %q", key)
	}
	return gameKey, leagueID, nil
}

// ParseTeamKey validates that key belongs to id and returns its numeric team.
func ParseTeamKey(key string, id Identity) (int, error) {
	parts := strings.Split(key, ".")
	if len(parts) != teamKeyParts || parts[1] != "l" || parts[3] != "t" {
		return 0, fmt.Errorf("invalid Yahoo team key %q", key)
	}
	gameKey, gameErr := positiveInt(parts[0])
	leagueID, leagueErr := positiveInt(parts[2])
	teamID, teamErr := positiveInt(parts[4])
	if gameErr != nil || leagueErr != nil || teamErr != nil || gameKey != id.GameKey || leagueID != id.LeagueID {
		return 0, fmt.Errorf("yahoo team key %q does not belong to league %s", key, id.LeagueKey)
	}
	return teamID, nil
}

// ParsePlayerKey validates that key belongs to the league's game.
func ParsePlayerKey(key string, id Identity) (int, error) {
	parts := strings.Split(key, ".")
	if len(parts) != playerKeyParts || parts[1] != "p" {
		return 0, fmt.Errorf("invalid Yahoo player key %q", key)
	}
	gameKey, gameErr := positiveInt(parts[0])
	playerID, playerErr := positiveInt(parts[2])
	if gameErr != nil || playerErr != nil || gameKey != id.GameKey {
		return 0, fmt.Errorf("yahoo player key %q does not belong to game %d", key, id.GameKey)
	}
	return playerID, nil
}

func positiveInt(value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("not a positive integer")
	}
	return parsed, nil
}

func findSeasonForKey(ctx context.Context, pool *pgxpool.Pool, key string, requested int) (int, error) {
	const query = `
SELECT season FROM yahoo_league_rule_snapshots
WHERE league_key = $1 AND ($2::integer = 0 OR season = $2)
ORDER BY last_seen_at DESC, id DESC LIMIT 1`
	var season int
	if err := pool.QueryRow(ctx, query, key, requested).Scan(&season); err != nil {
		if err == pgx.ErrNoRows {
			return 0, fmt.Errorf("league %s has no imported rule snapshot; sync its league metadata first", key)
		}
		return 0, fmt.Errorf("resolve league %s: %w", key, err)
	}
	return season, nil
}

func findIdentityForID(ctx context.Context, pool *pgxpool.Pool, leagueID, requestedSeason int) (Identity, error) {
	const query = `
SELECT league_key, season FROM yahoo_league_rule_snapshots
WHERE league_id = $1 AND ($2::integer = 0 OR season = $2)
ORDER BY last_seen_at DESC, id DESC LIMIT 1`
	var key string
	var season int
	if err := pool.QueryRow(ctx, query, leagueID, requestedSeason).Scan(&key, &season); err != nil {
		if err == pgx.ErrNoRows {
			return Identity{}, fmt.Errorf("league ID %d has no imported rule snapshot for season %d", leagueID, requestedSeason)
		}
		return Identity{}, fmt.Errorf("resolve league ID %d: %w", leagueID, err)
	}
	gameKey, parsedID, err := ParseLeagueKey(key)
	if err != nil || parsedID != leagueID {
		return Identity{}, fmt.Errorf("stored league key %q is inconsistent with league ID %d", key, leagueID)
	}
	return Identity{LeagueKey: key, Season: season, LeagueID: leagueID, GameKey: gameKey}, nil
}
