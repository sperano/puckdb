package yahoolink

import (
	"context"
	"errors"
	"fmt"

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
// for the guid or pair, of either source, becomes SourceManual: an operator
// linking it by hand always wins over a later email resync. Returns
// ErrUnknownTeam when no yahoo_teams row matches. The returned Link's
// TeamName, LeagueName and Season are always empty: call ListUserTeams for a
// display row.
func (s *PGStore) Link(ctx context.Context, userID pgtype.UUID, leagueID, teamID int32) (Link, error) {
	managers, err := s.queries.ListYahooTeamManagerGUIDs(ctx, sqlcdb.ListYahooTeamManagerGUIDsParams{
		LeagueID: leagueID, TeamID: teamID,
	})
	if err != nil {
		return Link{}, fmt.Errorf("resolve team's manager guids: %w", err)
	}
	if len(managers) == 0 {
		return Link{}, fmt.Errorf("%w: league %d team %d", ErrUnknownTeam, leagueID, teamID)
	}
	if guid, ok := singleGUID(managers); ok {
		row, err := s.queries.LinkAppUserYahooManager(ctx, sqlcdb.LinkAppUserYahooManagerParams{
			UserID: userID, Guid: guid,
		})
		if err != nil {
			return Link{}, fmt.Errorf("link yahoo account: %w", err)
		}
		return Link{LeagueID: leagueID, TeamID: teamID, GUID: row.Guid, Source: row.Source, LinkedAt: row.LinkedAt.Time}, nil
	}
	row, err := s.queries.LinkAppUserYahooTeam(ctx, sqlcdb.LinkAppUserYahooTeamParams{
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
