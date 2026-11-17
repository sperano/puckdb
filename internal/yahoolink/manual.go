package yahoolink

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// foreignKeyViolation is the PostgreSQL SQLSTATE a team link insert gets
// for a league/team pair with no yahoo_teams row (the team deleted since
// Link looked it up).
const foreignKeyViolation = "23503"

// Link adds or promotes a manual link between userID and the given Yahoo
// team: a guid link when every manager of the team resolves to one and the
// same guid, so the user also gets that account's teams of every other
// season; otherwise (no manager, an unresolved guid, a co-managed team
// whose managers resolve differently) a team link. A link already present
// for the guid or pair, of any source (email or self), becomes
// SourceManual: an operator linking it by hand always wins over a later
// email resync. Returns ErrUnknownTeam only when no yahoo_teams row matches
// (a team with no manager row gets a team link, as a pick of it does in
// Picker.SetTeams) and ErrUserNotFound for no such user. It also removes the
// user's deselection of the team (Picker.SetTeams), in the same
// transaction: the operator's link wins. It takes the user row lock first,
// so it serializes with SetTeams. The returned Link's TeamName, LeagueName
// and Season are always empty: call ListUserTeams for a display row.
func (s *PGStore) Link(ctx context.Context, userID pgtype.UUID, leagueID, teamID int32) (Link, error) {
	var linked Link
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		linked, err = link(ctx, sqlcdb.New(tx), userID, leagueID, teamID)
		return err
	})
	if err != nil {
		return Link{}, err
	}
	return linked, nil
}

// link is Link's body, over the transaction's queries q.
func link(ctx context.Context, q *sqlcdb.Queries, userID pgtype.UUID, leagueID, teamID int32) (Link, error) {
	if _, err := lockUser(ctx, q, userID); err != nil {
		return Link{}, err
	}
	managers, err := q.ListYahooTeamManagerGUIDs(ctx, sqlcdb.ListYahooTeamManagerGUIDsParams{
		LeagueID: leagueID, TeamID: teamID,
	})
	if err != nil {
		return Link{}, fmt.Errorf("resolve team's manager guids: %w", err)
	}
	if len(managers) == 0 {
		return Link{}, fmt.Errorf("%w: league %d team %d", ErrUnknownTeam, leagueID, teamID)
	}
	if err := clearRejections(ctx, q, userID, []teamKey{{leagueID, teamID}}); err != nil {
		return Link{}, err
	}
	if guid, ok := singleGUID(managers); ok {
		row, err := q.LinkAppUserYahooManager(ctx, sqlcdb.LinkAppUserYahooManagerParams{
			UserID: userID, Guid: guid,
		})
		if err != nil {
			return Link{}, fmt.Errorf("link yahoo account: %w", err)
		}
		return Link{LeagueID: leagueID, TeamID: teamID, GUID: row.Guid, Source: row.Source, LinkedAt: row.LinkedAt.Time}, nil
	}
	row, err := q.LinkAppUserYahooTeam(ctx, sqlcdb.LinkAppUserYahooTeamParams{
		UserID: userID, LeagueID: leagueID, TeamID: teamID,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return Link{}, fmt.Errorf("%w: league %d team %d", ErrUnknownTeam, leagueID, teamID)
		}
		return Link{}, fmt.Errorf("link team: %w", err)
	}
	return Link{LeagueID: row.LeagueID, TeamID: row.TeamID, Source: row.Source, LinkedAt: row.LinkedAt.Time}, nil
}

// singleGUID returns the guid every manager of a team resolves to, and
// false when the team has no manager, a manager with no guid, or managers
// resolving to different guids (a co-managed team: the operator could mean
// either manager).
func singleGUID(managers []sqlcdb.ListYahooTeamManagerGUIDsRow) (string, bool) {
	var guid string
	for i, m := range managers {
		if !m.ManagerID.Valid || !m.Guid.Valid || (i > 0 && m.Guid.String != guid) {
			return "", false
		}
		guid = m.Guid.String
	}
	return guid, guid != ""
}
