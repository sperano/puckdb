package yahoolink

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// SyncEmailLinks links userID to every Yahoo team whose yahoo_team_managers
// row's email matches email case-insensitively, as SourceEmail, and removes
// userID's own email-sourced links whose pair no longer matches (an email
// change, or the manager row's). It never touches SourceManual links. An
// empty email matches nothing, so it clears every email-sourced link.
//
// q must wrap the login transaction (appuser.PGStore.Resolve's), so the user
// row and its links commit together.
func SyncEmailLinks(ctx context.Context, q *sqlcdb.Queries, userID pgtype.UUID, email string) error {
	matches, err := q.ListManagerLeagueTeamsByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("find yahoo teams managed by %q: %w", email, err)
	}
	leagueIDs := make([]int32, len(matches))
	teamIDs := make([]int32, len(matches))
	for i, m := range matches {
		leagueIDs[i] = m.LeagueID
		teamIDs[i] = m.TeamID
	}
	if err := q.SyncAppUserEmailTeamLinks(ctx, sqlcdb.SyncAppUserEmailTeamLinksParams{
		UserID: userID, LeagueIds: leagueIDs, TeamIds: teamIDs,
	}); err != nil {
		return fmt.Errorf("sync email-sourced team links: %w", err)
	}
	if err := q.DeleteStaleAppUserEmailTeamLinks(ctx, sqlcdb.DeleteStaleAppUserEmailTeamLinksParams{
		UserID: userID, LeagueIds: leagueIDs, TeamIds: teamIDs,
	}); err != nil {
		return fmt.Errorf("delete stale email-sourced team links: %w", err)
	}
	return nil
}
