package news

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ErrNoYahooPools means no Yahoo league player pool was imported for the
// season, so there is no Yahoo status to read.
var ErrNoYahooPools = errors.New("no Yahoo league player pool imported")

// DirectoryQueries is the database access LoadDirectory and
// LoadYahooStatuses need.
type DirectoryQueries interface {
	ListNewsResolverPlayers(ctx context.Context) ([]sqlcdb.ListNewsResolverPlayersRow, error)
	ListNewsYahooPlayers(ctx context.Context, season int32) ([]sqlcdb.ListNewsYahooPlayersRow, error)
	ListNewsYahooPoolFetches(ctx context.Context, season int32) ([]sqlcdb.ListNewsYahooPoolFetchesRow, error)
	ListNewsTeams(ctx context.Context) ([]sqlcdb.ListNewsTeamsRow, error)
}

// LoadDirectory builds the player directory from the NHL players, the
// season's Yahoo league pools (one entry per player for all leagues) and the
// NHL team names.
func LoadDirectory(ctx context.Context, q DirectoryQueries, season int) (*Directory, error) {
	playerRows, err := q.ListNewsResolverPlayers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list players: %w", err)
	}
	yahooRows, err := q.ListNewsYahooPlayers(ctx, int32(season))
	if err != nil {
		return nil, fmt.Errorf("list Yahoo pool players of season %d: %w", season, err)
	}
	teamRows, err := q.ListNewsTeams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	players := make([]NHLPlayer, 0, len(playerRows))
	for _, r := range playerRows {
		players = append(players, NHLPlayer{
			ID: r.ID, YahooID: int(r.YahooID.Int64), FirstName: r.FirstName, LastName: r.LastName,
			Team: r.TeamAbbrev, Active: r.IsActive,
		})
	}
	yahooPlayers := make([]YahooPlayer, 0, len(yahooRows))
	for _, r := range yahooRows {
		yahooPlayers = append(yahooPlayers, YahooPlayer{
			YahooPlayerID: int(r.PlayerID), NHLPlayerID: r.NhlPlayerID.Int64, Name: r.FullName, Team: r.EditorialTeamAbbr,
		})
	}
	teams := make([]Team, 0, len(teamRows))
	for _, r := range teamRows {
		teams = append(teams, Team{Abbrev: r.Abbrev, FullName: r.FullName, CommonName: r.CommonName})
	}
	return NewDirectory(players, yahooPlayers, teams), nil
}

// LoadYahooStatuses reads each player's newest status across the season's
// league pools and the time the Yahoo data is current as of: the oldest of
// the leagues' latest pool fetches.
func LoadYahooStatuses(ctx context.Context, q DirectoryQueries, season int) ([]YahooStatus, time.Time, error) {
	fetches, err := q.ListNewsYahooPoolFetches(ctx, int32(season))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("list Yahoo pool fetches of season %d: %w", season, err)
	}
	if len(fetches) == 0 {
		return nil, time.Time{}, fmt.Errorf("season %d: %w; run a Yahoo sync first", season, ErrNoYahooPools)
	}
	var asOf time.Time
	for _, f := range fetches {
		if t := timeOf(f.FetchedAt); asOf.IsZero() || t.Before(asOf) {
			asOf = t
		}
	}
	rows, err := q.ListNewsYahooPlayers(ctx, int32(season))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("list Yahoo pool players of season %d: %w", season, err)
	}
	statuses := make([]YahooStatus, 0, len(rows))
	for _, r := range rows {
		statuses = append(statuses, YahooStatus{
			YahooPlayerID: int(r.PlayerID), Name: r.FullName, Status: r.Status, StatusFull: r.StatusFull,
			InjuryNote: r.InjuryNote, OnDisabledList: r.OnDisabledList, FetchedAt: timeOf(r.FetchedAt),
		})
	}
	return statuses, asOf, nil
}
