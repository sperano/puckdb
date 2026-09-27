// TEMPORARY: everything in this file exists only while Yahoo answers 403 for
// the 2026 leagues (see config.LeagueMetadataSource). A league whose latest
// rules snapshot is SourceTemporaryStandIn has no yahoo_league_players rows
// (the Yahoo players download never runs for it), so its draft ranking would
// otherwise fail with MISSING_POOL. LoadStandInPool builds a stand-in pool
// from NHL rosters instead. Delete this file together with
// config.LeagueMetadataSource once Yahoo access returns.
package draft

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// standInPlayerKeyPrefix marks a pool player key as synthesized from NHL
	// rosters rather than issued by Yahoo, so it can never collide with a
	// Yahoo player key ("<game_key>.p.<player_id>").
	standInPlayerKeyPrefix = "nhl.p."

	// standInExcludedListLimit caps how many excluded player names a note
	// lists by name; the count always covers every excluded player.
	standInExcludedListLimit = 50
)

// StandInPlayerKey is the synthetic, stable PlayerKey of an NHL player in a
// stand-in pool.
func StandInPlayerKey(nhlPlayerID int64) string {
	return fmt.Sprintf("%s%d", standInPlayerKeyPrefix, nhlPlayerID)
}

// ExcludedPlayer names a roster player left out of a stand-in pool.
type ExcludedPlayer struct {
	Name        string
	NHLPlayerID int64
}

// StandInPoolResult is a temporary NHL-roster stand-in for a league's Yahoo
// player pool: the players plus what had to be assumed or left out.
type StandInPoolResult struct {
	Players []PoolPlayer
	// RosterSeason is the NHL season ID (e.g. 20262027) the pool was built
	// from.
	RosterSeason int
	// FallbackReason is set, and says why, when the prior season's rosters
	// were used instead of the league's own season's (an incomplete or
	// missing import).
	FallbackReason string
	// HistoryFloorSeason and HistoryLastSeason are the oldest and newest NHL
	// season IDs (inclusive) the projection model reads a player's history
	// from for the league's target season; ExcludedNoHistory players have no
	// qualifying games in that range.
	HistoryFloorSeason int
	HistoryLastSeason  int
	// ExcludedNoPosition lists roster players left out because neither the
	// roster row nor players.position resolves to a usable position: they
	// cannot be placed on a roster slot or projected (the model requires a
	// position; see projection.validatePoolPlayer).
	ExcludedNoPosition []ExcludedPlayer
	// ExcludedNoHistory lists roster players left out for having no NHL
	// regular-season history in the model's window: they cannot be
	// projected (see ListSeasonRosterPoolCandidates and
	// projection.ValidateCoverage).
	ExcludedNoHistory []ExcludedPlayer
}

// Notes describes the pool's provenance and any exclusions, for a coverage
// or ranking report.
func (r StandInPoolResult) Notes() []string {
	notes := []string{fmt.Sprintf(
		"provisional pool from NHL %s rosters (Yahoo unavailable); one position per player",
		standInSeasonLabel(r.RosterSeason))}
	if r.FallbackReason != "" {
		notes = append(notes, "used the prior season's rosters: "+r.FallbackReason)
	}
	total := len(r.Players) + len(r.ExcludedNoPosition) + len(r.ExcludedNoHistory)
	if len(r.ExcludedNoPosition) > 0 {
		notes = append(notes, fmt.Sprintf(
			"excluded %d of %d roster players with no known position: %s",
			len(r.ExcludedNoPosition), total, excludedNames(r.ExcludedNoPosition)))
	}
	if len(r.ExcludedNoHistory) > 0 {
		notes = append(notes, fmt.Sprintf(
			"excluded %d of %d roster players with no NHL regular-season games in %s–%s: %s",
			len(r.ExcludedNoHistory), total,
			standInSeasonLabel(r.HistoryFloorSeason), standInSeasonLabel(r.HistoryLastSeason), excludedNames(r.ExcludedNoHistory)))
	}
	return notes
}

func excludedNames(excluded []ExcludedPlayer) string {
	names := make([]string, 0, min(len(excluded), standInExcludedListLimit+1))
	for i, p := range excluded {
		if i == standInExcludedListLimit {
			names = append(names, fmt.Sprintf("... and %d more", len(excluded)-standInExcludedListLimit))
			break
		}
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

// LoadStandInPool builds a league's temporary draftable pool from NHL
// rosters: players on the league's season's own season_rosters when every
// NHL club has at least one row there, otherwise the prior season's (see
// standInRosterSeason). A player whose position cannot be resolved (neither
// the roster row nor players.position names one), or who has no NHL
// regular-season history within the projection model's own lookback window
// (projection.DefaultConfig, the config the refresher projects with), is
// excluded — either would fail the whole league's projection otherwise —
// and reported instead of silently dropped. A player rostered by more than
// one team in the chosen season appears once, keeping the most recently
// updated roster row.
func LoadStandInPool(ctx context.Context, q Queries, leagueSeason int) (StandInPoolResult, error) {
	rosterSeason, fallbackReason, err := standInRosterSeason(ctx, q, leagueSeason)
	if err != nil {
		return StandInPoolResult{}, err
	}
	targetSeason := NHLSeasonID(leagueSeason)
	floor := projection.DefaultConfig().HistoryFloorSeason(targetSeason)
	last := NHLSeasonID(leagueSeason - 1)

	rows, err := q.ListSeasonRosterPoolCandidates(ctx, sqlcdb.ListSeasonRosterPoolCandidatesParams{
		Season: int32(rosterSeason), MinSeason: int32(floor), TargetSeason: int32(targetSeason),
	})
	if err != nil {
		return StandInPoolResult{}, fmt.Errorf("load NHL roster pool for season %d: %w", rosterSeason, err)
	}
	result := StandInPoolResult{
		RosterSeason: rosterSeason, FallbackReason: fallbackReason,
		HistoryFloorSeason: floor, HistoryLastSeason: last,
	}
	standInFillPool(&result, rows)
	return result, nil
}

// standInFillPool dedupes roster rows by player (most recently updated row
// wins) and splits them into projectable players and the two exclusion
// reasons.
func standInFillPool(result *StandInPoolResult, rows []sqlcdb.ListSeasonRosterPoolCandidatesRow) {
	result.Players = make([]PoolPlayer, 0, len(rows))
	result.ExcludedNoPosition = make([]ExcludedPlayer, 0, len(rows))
	result.ExcludedNoHistory = make([]ExcludedPlayer, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for _, row := range rows {
		// Rows are ordered player_id, updated_at DESC, team_id, so the first
		// row seen for a player is its most recently updated roster slot.
		if seen[row.PlayerID] {
			continue
		}
		seen[row.PlayerID] = true
		name := standInPlayerName(row)
		positions := standInEligiblePositions(row.Position)
		if len(positions) == 0 {
			result.ExcludedNoPosition = append(result.ExcludedNoPosition, ExcludedPlayer{Name: name, NHLPlayerID: row.PlayerID})
			continue
		}
		if !standInHasHistory(row) {
			result.ExcludedNoHistory = append(result.ExcludedNoHistory, ExcludedPlayer{Name: name, NHLPlayerID: row.PlayerID})
			continue
		}
		result.Players = append(result.Players, standInPoolPlayer(row, name, positions))
	}
	slices.SortFunc(result.Players, func(a, b PoolPlayer) int { return strings.Compare(a.PlayerKey, b.PlayerKey) })
	slices.SortFunc(result.ExcludedNoPosition, func(a, b ExcludedPlayer) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(result.ExcludedNoHistory, func(a, b ExcludedPlayer) int { return strings.Compare(a.Name, b.Name) })
}

// standInRosterSeason picks the roster season backing the pool: the
// league's own season when every one of its NHL clubs (season_teams,
// team_kind = 'nhl') has at least one imported season_rosters row,
// otherwise the prior season. A partial import — a cancelled sync, or camp
// rosters not all published yet — would otherwise silently rank a pool
// missing whole teams. fallbackReason is empty when the league's own season
// was used, and says why otherwise.
func standInRosterSeason(ctx context.Context, q Queries, leagueSeason int) (season int, fallbackReason string, err error) {
	own := NHLSeasonID(leagueSeason)
	coverage, err := q.GetSeasonRosterCoverage(ctx, int32(own))
	if err != nil {
		return 0, "", fmt.Errorf("check season %d roster coverage: %w", own, err)
	}
	fallback := NHLSeasonID(leagueSeason - 1)
	switch {
	case coverage.NhlTeams == 0:
		return fallback, fmt.Sprintf("%s has no NHL teams imported", standInSeasonLabel(own)), nil
	case coverage.TeamsWithRosters < coverage.NhlTeams:
		return fallback, fmt.Sprintf("%s rosters incomplete: %d of %d teams",
			standInSeasonLabel(own), coverage.TeamsWithRosters, coverage.NhlTeams), nil
	default:
		return own, "", nil
	}
}

// standInSeasonLabel formats an NHL season ID (e.g. 20262027) the way NHL
// broadcasts do ("2026-27"), for a human-readable note.
func standInSeasonLabel(seasonID int) string {
	return SeasonLabel(seasonID)
}

func standInPlayerName(row sqlcdb.ListSeasonRosterPoolCandidatesRow) string {
	return strings.TrimSpace(row.FirstName + " " + row.LastName)
}

// standInHasHistory reports whether the player has NHL regular-season
// history within the projection model's lookback window, in the table the
// model reads for their kind: goalies from game_goalie_stats, every other
// resolved position from game_skater_stats. ListSeasonRosterPoolCandidates
// already restricts has_skater_history/has_goalie_history to that window
// (see ListProjectionSkaterHistory/ListProjectionGoalieHistory in
// internal/sqlcdb/queries/projections.sql), so a player this returns false
// for would also fail projection coverage if included.
func standInHasHistory(row sqlcdb.ListSeasonRosterPoolCandidatesRow) bool {
	if row.Position.Valid && row.Position.PlayerPosition == sqlcdb.PlayerPositionG {
		return row.HasGoalieHistory
	}
	return row.HasSkaterHistory
}

// standInPoolPlayer maps one roster row to a pool player. YahooPlayerID is
// players.yahoo_id when a later Yahoo match set it, or 0.
func standInPoolPlayer(row sqlcdb.ListSeasonRosterPoolCandidatesRow, name string, positions []string) PoolPlayer {
	var yahooID int
	if row.YahooID.Valid {
		yahooID = int(row.YahooID.Int64)
	}
	return PoolPlayer{
		PlayerKey: StandInPlayerKey(row.PlayerID), NHLPlayerID: row.PlayerID, YahooPlayerID: yahooID,
		Name: name, Team: row.TeamAbbrev, EligiblePositions: positions,
		FetchedAt: row.UpdatedAt.Time,
	}
}

// standInEligiblePositions maps a resolved position (season_rosters.position
// preferred, players.position when the roster didn't say — see
// ListSeasonRosterPoolCandidates) to the single Yahoo-style position it
// corresponds to. nhl-api-go already normalizes NHL position codes to these
// same values (player_position enum: C, LW, RW, D, G); "F" (an unspecified
// forward) and any other value resolve to no position, which the caller
// must exclude rather than pass through unpositioned.
func standInEligiblePositions(position sqlcdb.NullPlayerPosition) []string {
	if !position.Valid {
		return nil
	}
	switch position.PlayerPosition {
	case sqlcdb.PlayerPositionC:
		return []string{PositionCenter}
	case sqlcdb.PlayerPositionLW:
		return []string{PositionLeftWing}
	case sqlcdb.PlayerPositionRW:
		return []string{PositionRightWing}
	case sqlcdb.PlayerPositionD:
		return []string{PositionDefense}
	case sqlcdb.PlayerPositionG:
		return []string{PositionGoalie}
	default:
		return nil
	}
}
