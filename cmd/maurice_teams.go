package cmd

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/maurice"
	"github.com/sperano/puckdb/internal/yahoolink"
)

// mauriceTeamLister adapts yahoolink.PGStore to maurice.TeamLister, limiting
// the teams Maurice names to leagueIDs (empty = every league, the instance's
// --maurice-yahoo-leagues allowlist).
type mauriceTeamLister struct {
	store     *yahoolink.PGStore
	leagueIDs []int32
}

// newMauriceTeamLister returns a maurice.TeamLister over pool, scoped to
// leagueIDs.
func newMauriceTeamLister(pool *pgxpool.Pool, leagueIDs []int32) maurice.TeamLister {
	return mauriceTeamLister{store: yahoolink.NewPGStore(pool), leagueIDs: leagueIDs}
}

// ListUserTeams implements maurice.TeamLister.
func (m mauriceTeamLister) ListUserTeams(ctx context.Context, userID string) ([]maurice.UserTeam, error) {
	var id pgtype.UUID
	if err := id.Scan(userID); err != nil {
		return nil, fmt.Errorf("invalid user id %q: %w", userID, err)
	}
	links, err := m.store.ListUserTeams(ctx, id, m.leagueIDs)
	if err != nil {
		return nil, err
	}
	teams := make([]maurice.UserTeam, len(links))
	for i, l := range links {
		teams[i] = maurice.UserTeam{LeagueID: l.LeagueID, TeamID: l.TeamID, TeamName: l.TeamName}
	}
	return teams, nil
}
