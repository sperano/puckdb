// Package yahoolink links a Maurice user (internal/appuser) to the Yahoo
// fantasy teams they manage, several per user, through two kinds of link:
//
//   - a guid link (app_user_yahoo_managers) names a Yahoo account, the
//     manager guid: the user's teams are every team whose manager resolves
//     to that guid (the yahoo_team_manager_guids view), in every season, so
//     a new season's teams appear once imported, with no new link;
//   - a team link (app_user_yahoo_teams) names one (league, team) pair, the
//     fallback for a manager row that resolves to no guid, or a co-managed
//     team an operator links by hand.
//
// Yahoo hides the guid ('--hidden--') of every current-season manager, so
// such a row resolves to the one real guid older rows with the same email
// carry, and to none for a manager with no earlier season (see
// yahoo_team_manager_guids in its migration).
//
// A link's source is either SourceEmail (the login email matched a
// yahoo_team_managers.email, case-insensitively, kept in sync at every
// authenticated request by SyncEmailLinks) or SourceManual (an operator
// added it for a user whose Yahoo email differs from their login email).
// league_id/team_id are the legacy numeric Yahoo identifiers yahoo_teams and
// yahoo_team_managers already use.
package yahoolink

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// SourceEmail marks a link made automatically because the login email
	// matched a yahoo_team_managers.email.
	SourceEmail = "email"
	// SourceManual marks a link an operator added by hand.
	SourceManual = "manual"
)

// ErrUnknownTeam is returned by Link for a (league, team) pair with no
// yahoo_teams row.
var ErrUnknownTeam = errors.New("unknown Yahoo league/team")

// ErrUserNotFound is returned when an admin lookup matches no user.
var ErrUserNotFound = errors.New("no such user")

// ErrAmbiguousUser is returned when an admin lookup matches more than one
// user: username is a mutable snapshot, not a unique key.
var ErrAmbiguousUser = errors.New("username matches more than one user")

// ErrEmailLinkStillMatches is returned by Unlink for a link, of either
// source, the given login email still matches: removing it would only be
// undone by SyncEmailLinks, as SourceEmail, on the user's next
// authenticated request.
var ErrEmailLinkStillMatches = errors.New("link still matches the login email")

// Link is one Yahoo team a user manages. GUID is the Yahoo account of the
// guid link the team comes through, empty for a team link; Source and
// LinkedAt are that link's. LeagueName and Season (the league's
// yahoo_leagues.season, Yahoo's start year: 2026 for 2026-27) are filled by
// ListUserTeams only.
type Link struct {
	LeagueID   int32
	TeamID     int32
	TeamName   string
	LeagueName string
	Season     int32
	GUID       string
	Source     string
	LinkedAt   time.Time
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

// PGStore reads and writes app_user_yahoo_managers and app_user_yahoo_teams.
type PGStore struct {
	queries *sqlcdb.Queries
}

// NewPGStore returns a PGStore over db, which may be a pool or a
// transaction (sqlcdb.DBTX).
func NewPGStore(db sqlcdb.DBTX) *PGStore {
	return &PGStore{queries: sqlcdb.New(db)}
}

// ListUserTeams returns userID's teams, through either kind of link and
// each once, with their league's name and season, limited to leagueIDs when it is non-empty (the instance's Yahoo
// league allowlist; empty means every league), ordered by league then team.
func (s *PGStore) ListUserTeams(ctx context.Context, userID pgtype.UUID, leagueIDs []int32) ([]Link, error) {
	rows, err := s.queries.ListAppUserYahooTeams(ctx, sqlcdb.ListAppUserYahooTeamsParams{
		UserID: userID, LeagueIds: leagueIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("list user's yahoo teams: %w", err)
	}
	links := make([]Link, len(rows))
	for i, r := range rows {
		links[i] = Link{
			LeagueID: r.LeagueID, TeamID: r.TeamID, TeamName: r.TeamName,
			LeagueName: r.LeagueName, Season: r.Season,
			GUID: r.Guid, Source: r.Source, LinkedAt: r.LinkedAt.Time,
		}
	}
	return links, nil
}

// ListInstanceLinks returns every user's teams on instance, each once as in
// ListUserTeams, for the admin list command.
func (s *PGStore) ListInstanceLinks(ctx context.Context, instance string) ([]InstanceLink, error) {
	rows, err := s.queries.ListInstanceAppUserYahooTeams(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("list instance's yahoo team links: %w", err)
	}
	links := make([]InstanceLink, len(rows))
	for i, r := range rows {
		links[i] = InstanceLink{
			Username: r.Username,
			Link: Link{
				LeagueID: r.LeagueID, TeamID: r.TeamID, TeamName: r.TeamName,
				GUID: r.Guid, Source: r.Source, LinkedAt: r.LinkedAt.Time,
			},
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
