package yahoolink

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// Unlinked reports what Unlink removed: the team link, the guid links
// through which the user reached the team (with every other team of those
// accounts, in every season), and the user's deselection of the team
// (Rejection).
type Unlinked struct {
	Team      bool
	GUIDs     []string
	Rejection bool
}

// Removed reports whether Unlink removed anything.
func (u Unlinked) Removed() bool { return u.Team || len(u.GUIDs) > 0 || u.Rejection }

// Unlink removes every link through which userID reaches the given Yahoo
// team, of either source: its team link, and its guid links to the team's
// managers, which also removes those accounts' other teams. It refuses, with
// ErrEmailLinkStillMatches and removing nothing, when the login email
// (email) still matches a manager of that team or resolves to a guid one of
// the team's managers resolves to, held or not: a manual link gets no
// exemption, since an operator's Link promotes a matching email link to
// manual (and SyncEmailLinks would just recreate it, as SourceEmail, on the
// user's next authenticated request either way); fix the
// yahoo_team_managers row's email instead. The checks and the deletes run
// in one transaction, so a failure removes nothing. Unless the email still
// matches, it also removes the user's deselection of the team
// (Picker.SetTeams), which no link reaches any more; while the email
// matches, the deselection stays, since removing it would let the resync
// link the team again. A zero Unlinked means the user had no link to the
// team, nor a deselection to remove; GUIDs lists only the links this call
// deleted, sorted.
func (s *PGStore) Unlink(ctx context.Context, userID pgtype.UUID, email string, leagueID, teamID int32) (Unlinked, error) {
	var removed Unlinked
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		removed, err = unlink(ctx, sqlcdb.New(tx), userID, email, leagueID, teamID)
		return err
	})
	if err != nil {
		return Unlinked{}, err
	}
	return removed, nil
}

// unlink is Unlink's body, over the transaction's queries q.
func unlink(ctx context.Context, q *sqlcdb.Queries, userID pgtype.UUID, email string, leagueID, teamID int32) (Unlinked, error) {
	held, teamGUIDs, err := heldLinks(ctx, q, userID, leagueID, teamID)
	if err != nil {
		return Unlinked{}, err
	}
	match, err := matchEmail(ctx, q, email)
	if err != nil {
		return Unlinked{}, err
	}
	if match.coversTeam(leagueID, teamID) || slices.ContainsFunc(teamGUIDs, match.hasGUID) {
		if !held.Removed() {
			return Unlinked{}, nil
		}
		return Unlinked{}, fmt.Errorf("%w: league %d team %d", ErrEmailLinkStillMatches, leagueID, teamID)
	}
	n, err := q.ClearAppUserYahooTeamRejections(ctx, sqlcdb.ClearAppUserYahooTeamRejectionsParams{
		UserID: userID, LeagueIds: []int32{leagueID}, TeamIds: []int32{teamID},
	})
	if err != nil {
		return Unlinked{}, fmt.Errorf("clear team deselection: %w", err)
	}
	removed := Unlinked{Rejection: n > 0}
	if held.Team {
		n, err := q.UnlinkAppUserYahooTeam(ctx, sqlcdb.UnlinkAppUserYahooTeamParams{
			UserID: userID, LeagueID: leagueID, TeamID: teamID,
		})
		if err != nil {
			return Unlinked{}, fmt.Errorf("unlink team: %w", err)
		}
		removed.Team = n > 0
	}
	if len(held.GUIDs) > 0 {
		guids, err := q.UnlinkAppUserYahooManagers(ctx, sqlcdb.UnlinkAppUserYahooManagersParams{
			UserID: userID, Guids: held.GUIDs,
		})
		if err != nil {
			return Unlinked{}, fmt.Errorf("unlink yahoo accounts: %w", err)
		}
		if len(guids) > 0 {
			slices.Sort(guids)
			removed.GUIDs = guids
		}
	}
	return removed, nil
}

// heldLinks returns the links through which userID reaches the team (its
// team link, and its guid links among the team's guids) and the team's
// guids: every guid its managers resolve to.
func heldLinks(ctx context.Context, q *sqlcdb.Queries, userID pgtype.UUID, leagueID, teamID int32) (Unlinked, []string, error) {
	hasTeam, err := q.HasAppUserYahooTeamLink(ctx, sqlcdb.HasAppUserYahooTeamLinkParams{
		UserID: userID, LeagueID: leagueID, TeamID: teamID,
	})
	if err != nil {
		return Unlinked{}, nil, fmt.Errorf("check team link: %w", err)
	}
	managers, err := q.ListYahooTeamManagerGUIDs(ctx, sqlcdb.ListYahooTeamManagerGUIDsParams{
		LeagueID: leagueID, TeamID: teamID,
	})
	if err != nil {
		return Unlinked{}, nil, fmt.Errorf("resolve team's manager guids: %w", err)
	}
	var teamGUIDs []string
	for _, m := range managers {
		if m.Guid.Valid && !slices.Contains(teamGUIDs, m.Guid.String) {
			teamGUIDs = append(teamGUIDs, m.Guid.String)
		}
	}
	guids, err := q.ListAppUserYahooManagerLinksAmong(ctx, sqlcdb.ListAppUserYahooManagerLinksAmongParams{
		UserID: userID, Guids: teamGUIDs,
	})
	if err != nil {
		return Unlinked{}, nil, fmt.Errorf("list held yahoo account links: %w", err)
	}
	return Unlinked{Team: hasTeam, GUIDs: guids}, teamGUIDs, nil
}
