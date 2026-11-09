// Package yahoolink links a Maurice user (internal/appuser) to the Yahoo
// fantasy teams they manage, several per user. A link's source is either
// SourceEmail (the login email matched a yahoo_team_managers.email,
// case-insensitively, kept in sync at every login by SyncEmailLinks) or
// SourceManual (an operator added it for a user whose Yahoo email differs
// from their login email). league_id/team_id are the legacy numeric Yahoo
// identifiers yahoo_teams and yahoo_team_managers already use.
package yahoolink

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// SourceEmail marks a link made automatically because the login email
	// matched a yahoo_team_managers.email.
	SourceEmail = "email"
	// SourceManual marks a link an operator added by hand.
	SourceManual = "manual"

	// foreignKeyViolation is the PostgreSQL SQLSTATE a Link call gets for a
	// league/team pair with no yahoo_teams row.
	foreignKeyViolation = "23503"
)

// ErrUnknownTeam is returned by Link for a (league, team) pair with no
// yahoo_teams row.
var ErrUnknownTeam = errors.New("unknown Yahoo league/team")

// ErrUserNotFound is returned when an admin lookup matches no user.
var ErrUserNotFound = errors.New("no such user")

// ErrAmbiguousUser is returned when an admin lookup matches more than one
// user: username is a mutable snapshot, not a unique key.
var ErrAmbiguousUser = errors.New("username matches more than one user")

// Link is one Yahoo team a user manages.
type Link struct {
	LeagueID int32
	TeamID   int32
	TeamName string
	Source   string
	LinkedAt time.Time
}

// User identifies the app_users row an admin command resolved by username.
type User struct {
	ID       pgtype.UUID
	Username string
	Email    string
}

// InstanceLink is one Link together with its owner's username, for the
// admin list command (which is not scoped to one user).
type InstanceLink struct {
	Username string
	Link     Link
}

// PGStore reads and writes app_user_yahoo_teams.
type PGStore struct {
	queries *sqlcdb.Queries
}

// NewPGStore returns a PGStore over db, which may be a pool or a
// transaction (sqlcdb.DBTX).
func NewPGStore(db sqlcdb.DBTX) *PGStore {
	return &PGStore{queries: sqlcdb.New(db)}
}

// ListUserTeams returns userID's linked teams, limited to leagueIDs when it
// is non-empty (the instance's Yahoo league allowlist; empty means every
// league), ordered by league then team.
func (s *PGStore) ListUserTeams(ctx context.Context, userID pgtype.UUID, leagueIDs []int32) ([]Link, error) {
	rows, err := s.queries.ListAppUserYahooTeams(ctx, sqlcdb.ListAppUserYahooTeamsParams{
		UserID: userID, LeagueIds: leagueIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("list user's yahoo teams: %w", err)
	}
	links := make([]Link, len(rows))
	for i, r := range rows {
		links[i] = Link{LeagueID: r.LeagueID, TeamID: r.TeamID, TeamName: r.TeamName, Source: r.Source, LinkedAt: r.LinkedAt.Time}
	}
	return links, nil
}

// ListInstanceLinks returns every user's linked teams on instance, for the
// admin list command.
func (s *PGStore) ListInstanceLinks(ctx context.Context, instance string) ([]InstanceLink, error) {
	rows, err := s.queries.ListInstanceAppUserYahooTeams(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("list instance's yahoo team links: %w", err)
	}
	links := make([]InstanceLink, len(rows))
	for i, r := range rows {
		links[i] = InstanceLink{
			Username: r.Username,
			Link:     Link{LeagueID: r.LeagueID, TeamID: r.TeamID, TeamName: r.TeamName, Source: r.Source, LinkedAt: r.LinkedAt.Time},
		}
	}
	return links, nil
}

// FindUser resolves instance's user named username, for the admin link
// commands. It returns ErrUserNotFound or ErrAmbiguousUser.
func (s *PGStore) FindUser(ctx context.Context, instance, username string) (User, error) {
	rows, err := s.queries.FindAppUserByInstanceUsername(ctx, sqlcdb.FindAppUserByInstanceUsernameParams{
		Instance: instance, Username: username,
	})
	if err != nil {
		return User{}, fmt.Errorf("find user %q: %w", username, err)
	}
	switch len(rows) {
	case 0:
		return User{}, fmt.Errorf("%w: %q on instance %q", ErrUserNotFound, username, instance)
	case 1:
		return User{ID: rows[0].ID, Username: rows[0].Username, Email: rows[0].Email.String}, nil
	default:
		return User{}, fmt.Errorf("%w: %q on instance %q", ErrAmbiguousUser, username, instance)
	}
}

// Link adds or promotes a manual link between userID and the given Yahoo
// team. A link already present for the pair, of either source, becomes
// SourceManual: an operator linking it by hand always wins over a later
// email resync. Returns ErrUnknownTeam when no yahoo_teams row matches.
// The returned Link's TeamName is always empty: call ListUserTeams for a
// display row.
func (s *PGStore) Link(ctx context.Context, userID pgtype.UUID, leagueID, teamID int32) (Link, error) {
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

// Unlink removes userID's link to the given Yahoo team, of either source.
// It reports whether a link existed.
func (s *PGStore) Unlink(ctx context.Context, userID pgtype.UUID, leagueID, teamID int32) (bool, error) {
	n, err := s.queries.UnlinkAppUserYahooTeam(ctx, sqlcdb.UnlinkAppUserYahooTeamParams{
		UserID: userID, LeagueID: leagueID, TeamID: teamID,
	})
	if err != nil {
		return false, fmt.Errorf("unlink team: %w", err)
	}
	return n > 0, nil
}

// ParseLeagueIDs reads a comma-separated list of numeric Yahoo league IDs,
// such as "29249,1001". An empty list is valid and means every league.
func ParseLeagueIDs(list string) ([]int32, error) {
	var ids []int32
	for _, field := range splitNonEmpty(list) {
		id, err := parseLeagueID(field)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
