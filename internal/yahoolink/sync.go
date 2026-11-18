package yahoolink

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// SyncEmailLinks links userID to every Yahoo account and team whose
// yahoo_team_managers row's email matches one of emails case-insensitively
// (the login email, and the members file's Yahoo email for it when it has
// one), as SourceEmail: a guid link for a row that resolves to a guid, a team
// link for one that does not. It removes userID's own email-sourced links the
// match no longer gives (an email change, the manager row's, or a row that
// now resolves to a guid, whose team link gives way to the guid link). It
// never touches SourceManual or SourceSelf links, and makes no team link to
// a team the user deselected (Picker.SetTeams). An empty email matches
// nothing, so no emails, or only empty ones, clear every email-sourced link.
//
// q must wrap the login transaction (appuser.PGStore.Resolve's), so the user
// row and its links commit together.
func SyncEmailLinks(ctx context.Context, q *sqlcdb.Queries, userID pgtype.UUID, emails ...string) error {
	match, err := matchEmails(ctx, q, emails)
	if err != nil {
		return err
	}
	if err := q.SyncAppUserEmailManagerLinks(ctx, sqlcdb.SyncAppUserEmailManagerLinksParams{
		UserID: userID, Guids: match.guids,
	}); err != nil {
		return fmt.Errorf("sync email-sourced yahoo account links: %w", err)
	}
	if err := q.DeleteStaleAppUserEmailManagerLinks(ctx, sqlcdb.DeleteStaleAppUserEmailManagerLinksParams{
		UserID: userID, Guids: match.guids,
	}); err != nil {
		return fmt.Errorf("delete stale email-sourced yahoo account links: %w", err)
	}
	if err := q.SyncAppUserEmailTeamLinks(ctx, sqlcdb.SyncAppUserEmailTeamLinksParams{
		UserID: userID, LeagueIds: match.leagueIDs, TeamIds: match.teamIDs,
	}); err != nil {
		return fmt.Errorf("sync email-sourced team links: %w", err)
	}
	if err := q.DeleteStaleAppUserEmailTeamLinks(ctx, sqlcdb.DeleteStaleAppUserEmailTeamLinksParams{
		UserID: userID, LeagueIds: match.leagueIDs, TeamIds: match.teamIDs,
	}); err != nil {
		return fmt.Errorf("delete stale email-sourced team links: %w", err)
	}
	return nil
}

// emailMatch is what an email matches among yahoo_team_managers: the guids
// its rows resolve to, the (league, team) pairs of its rows that resolve to
// none (leagueIDs[i], teamIDs[i]), and every pair it matches either way.
type emailMatch struct {
	guids     []string
	leagueIDs []int32
	teamIDs   []int32
	all       []teamKey
}

type teamKey struct{ leagueID, teamID int32 }

// matchEmails reads the manager rows any of emails matches (none for an
// empty email) and splits them by whether they resolve to a guid. The slices
// are never nil: the sync queries compare against them with ANY.
func matchEmails(ctx context.Context, q *sqlcdb.Queries, emails []string) (emailMatch, error) {
	m := emailMatch{guids: []string{}, leagueIDs: []int32{}, teamIDs: []int32{}}
	var unresolved []teamKey
	for _, email := range emails {
		if email == "" {
			continue
		}
		rows, err := q.ListManagersByEmail(ctx, email)
		if err != nil {
			return emailMatch{}, fmt.Errorf("find yahoo managers with email %q: %w", email, err)
		}
		for _, r := range rows {
			unresolved = m.add(r, unresolved)
		}
	}
	return m, nil
}

// add records one manager row in m; unresolved holds the pairs already
// recorded as resolving to no guid, and add returns it updated.
func (m *emailMatch) add(r sqlcdb.ListManagersByEmailRow, unresolved []teamKey) []teamKey {
	key := teamKey{r.LeagueID, r.TeamID}
	if !slices.Contains(m.all, key) {
		m.all = append(m.all, key)
	}
	switch {
	case r.Guid.Valid:
		if !slices.Contains(m.guids, r.Guid.String) {
			m.guids = append(m.guids, r.Guid.String)
		}
	case !slices.Contains(unresolved, key):
		unresolved = append(unresolved, key)
		m.leagueIDs = append(m.leagueIDs, r.LeagueID)
		m.teamIDs = append(m.teamIDs, r.TeamID)
	}
	return unresolved
}

// coversTeam reports whether the emails match a manager of the team.
func (m emailMatch) coversTeam(leagueID, teamID int32) bool {
	return slices.Contains(m.all, teamKey{leagueID, teamID})
}

// hasGUID reports whether one of the emails' rows resolves to guid.
func (m emailMatch) hasGUID(guid string) bool { return slices.Contains(m.guids, guid) }
