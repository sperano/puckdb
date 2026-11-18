package yahoolink

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ErrTeamNotOffered is returned by Picker.SetTeams for a team outside the
// choices: another season's, a league outside the allowlist, or no team.
var ErrTeamNotOffered = errors.New("team is not among the team choices")

// TeamRef names one Yahoo team by its numeric league and team IDs.
type TeamRef struct {
	LeagueID int32
	TeamID   int32
}

// TeamChoice is one team a user may pick. ManagerNicknames are the team's
// managers' Yahoo nicknames, never their emails or guids. Source is the
// source of the link the user reaches the team through (as ListUserTeams
// picks it), empty when none does.
type TeamChoice struct {
	LeagueID         int32
	LeagueName       string
	TeamID           int32
	TeamName         string
	ManagerNicknames []string
	Source           string
}

// Linked reports whether the user reaches the team through a link.
func (c TeamChoice) Linked() bool { return c.Source != "" }

// TeamChoices is what a Picker offers a user: the teams of Season (a Yahoo
// start year; 0, with no teams, when no league of the allowlist has a
// season up to the current one), ordered by league then team, and whether
// the user already saved or dismissed the first-login offer (Offered;
// false again after PGStore.ResetTeamOffer or ResetInstanceTeamOffers).
type TeamChoices struct {
	Season  int32
	Teams   []TeamChoice
	Offered bool
}

// Picker lets a user pick their own Yahoo teams among the teams of the
// default season (DefaultSeason of the allowlist's league seasons against
// the current NHL season) in the instance's Yahoo league allowlist (empty:
// every league). A pick is self-declared and unverified: it only changes
// the user's own teams, and several users may pick the same team
// (co-managers).
type Picker struct {
	store     *PGStore
	leagueIDs []int32
	// currentSeason returns the current NHL season as a Yahoo start year.
	currentSeason func() int32
}

// NewPicker returns a Picker over db for the leagueIDs allowlist;
// currentSeason returns the current NHL season as a Yahoo start year
// (CurrentSeason in production).
func NewPicker(db DB, leagueIDs []int32, currentSeason func() int32) *Picker {
	return &Picker{store: NewPGStore(db), leagueIDs: leagueIDs, currentSeason: currentSeason}
}

// TeamChoices returns the teams userID (an app_users ID) may pick.
func (p *Picker) TeamChoices(ctx context.Context, userID string) (TeamChoices, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return TeamChoices{}, err
	}
	return p.choices(ctx, p.store.queries, id)
}

// SetTeams makes teams userID's selection among the choices and marks the
// offer as seen, in one transaction. A picked team the user reaches through
// no link, or only through an email link, gets a SourceSelf link, of the
// same kind Link makes (a guid link when every manager of the team resolves
// to one guid, which also reaches that account's teams of other seasons; a
// team link otherwise), so the pick outlives the email match; a team the
// user reaches through a manual or self link keeps it. A choice the user reaches but did
// not pick is deselected: hidden from the user's teams whatever link
// reaches it, and never relinked by the email resync. Links outside the
// choices are untouched. It returns ErrTeamNotOffered, changing nothing,
// for a team outside the choices.
func (p *Picker) SetTeams(ctx context.Context, userID string, teams []TeamRef) (TeamChoices, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return TeamChoices{}, err
	}
	var result TeamChoices
	err = pgx.BeginFunc(ctx, p.store.db, func(tx pgx.Tx) error {
		q := sqlcdb.New(tx)
		// First, so the user row lock serializes this with the user's
		// logins (their email resync) and other selections.
		if err := markOfferSeen(ctx, q, id); err != nil {
			return err
		}
		if err := p.setTeams(ctx, q, id, teams); err != nil {
			return err
		}
		result, err = p.choices(ctx, q, id)
		return err
	})
	if err != nil {
		return TeamChoices{}, err
	}
	return result, nil
}

// DismissTeamOffer marks the offer as seen for userID, changing no link.
func (p *Picker) DismissTeamOffer(ctx context.Context, userID string) error {
	id, err := parseUserID(userID)
	if err != nil {
		return err
	}
	return markOfferSeen(ctx, p.store.queries, id)
}

// setTeams is SetTeams' body, over the transaction's queries q.
func (p *Picker) setTeams(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID, teams []TeamRef) error {
	offered, err := p.offeredTeams(ctx, q)
	if err != nil {
		return err
	}
	wanted, err := wantedTeams(teams, offered)
	if err != nil {
		return err
	}
	if err := clearRejections(ctx, q, id, wanted); err != nil {
		return err
	}
	linked, err := p.linkedSources(ctx, q, id)
	if err != nil {
		return err
	}
	for _, k := range wanted {
		if source, ok := linked[k]; !ok || source == SourceEmail {
			if err := linkSelf(ctx, q, id, k); err != nil {
				return err
			}
		}
	}
	// Again: a guid link made above may reach other choices.
	linked, err = p.linkedSources(ctx, q, id)
	if err != nil {
		return err
	}
	var rejected []teamKey
	for _, k := range offered {
		if _, ok := linked[k]; ok && !slices.Contains(wanted, k) {
			rejected = append(rejected, k)
		}
	}
	return rejectTeams(ctx, q, id, rejected)
}

// wantedTeams returns teams without duplicates, in order, or
// ErrTeamNotOffered for the first one outside offered.
func wantedTeams(teams []TeamRef, offered []teamKey) ([]teamKey, error) {
	var wanted []teamKey
	for _, t := range teams {
		k := teamKey{t.LeagueID, t.TeamID}
		if !slices.Contains(offered, k) {
			return nil, fmt.Errorf("%w: league %d team %d", ErrTeamNotOffered, t.LeagueID, t.TeamID)
		}
		if !slices.Contains(wanted, k) {
			wanted = append(wanted, k)
		}
	}
	return wanted, nil
}

// choices builds the user's TeamChoices over q.
func (p *Picker) choices(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID) (TeamChoices, error) {
	offered, err := q.GetAppUserTeamOfferSeen(ctx, id)
	if err != nil {
		return TeamChoices{}, fmt.Errorf("read team offer flag: %w", err)
	}
	season, rows, err := p.choiceRows(ctx, q)
	if err != nil {
		return TeamChoices{}, err
	}
	linked, err := p.linkedSources(ctx, q, id)
	if err != nil {
		return TeamChoices{}, err
	}
	result := TeamChoices{Season: season, Teams: make([]TeamChoice, len(rows)), Offered: offered}
	for i, r := range rows {
		result.Teams[i] = TeamChoice{
			LeagueID: r.LeagueID, LeagueName: r.LeagueName, TeamID: r.TeamID, TeamName: r.TeamName,
			ManagerNicknames: r.ManagerNicknames, Source: linked[teamKey{r.LeagueID, r.TeamID}],
		}
	}
	return result, nil
}

// choiceRows returns the default season of the allowlist's leagues and its
// teams, or 0 and none when no league has a season up to the current one.
func (p *Picker) choiceRows(ctx context.Context, q *sqlcdb.Queries) (int32, []sqlcdb.ListYahooTeamChoicesRow, error) {
	seasons, err := q.ListYahooLeagueSeasons(ctx, p.leagueIDs)
	if err != nil {
		return 0, nil, fmt.Errorf("list yahoo league seasons: %w", err)
	}
	season, ok := DefaultSeason(seasons, p.currentSeason())
	if !ok {
		return 0, nil, nil
	}
	rows, err := q.ListYahooTeamChoices(ctx, sqlcdb.ListYahooTeamChoicesParams{Season: season, LeagueIds: p.leagueIDs})
	if err != nil {
		return 0, nil, fmt.Errorf("list yahoo team choices: %w", err)
	}
	return season, rows, nil
}

// offeredTeams returns the choices' teams, in order.
func (p *Picker) offeredTeams(ctx context.Context, q *sqlcdb.Queries) ([]teamKey, error) {
	_, rows, err := p.choiceRows(ctx, q)
	if err != nil {
		return nil, err
	}
	keys := make([]teamKey, len(rows))
	for i, r := range rows {
		keys[i] = teamKey{r.LeagueID, r.TeamID}
	}
	return keys, nil
}

// linkedSources maps each team of the allowlist the user reaches to the
// source of the link ListUserTeams picks for it.
func (p *Picker) linkedSources(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID) (map[teamKey]string, error) {
	links, err := listUserTeams(ctx, q, id, p.leagueIDs)
	if err != nil {
		return nil, err
	}
	sources := make(map[teamKey]string, len(links))
	for _, l := range links {
		sources[teamKey{l.LeagueID, l.TeamID}] = l.Source
	}
	return sources, nil
}

// linkSelf links the user to the team as SourceSelf, as Link would: a guid
// link when every manager resolves to one guid, a team link otherwise. An
// email link already present for the guid or pair becomes SourceSelf; a
// manual or self one stays.
func linkSelf(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID, k teamKey) error {
	managers, err := q.ListYahooTeamManagerGUIDs(ctx, sqlcdb.ListYahooTeamManagerGUIDsParams{
		LeagueID: k.leagueID, TeamID: k.teamID,
	})
	if err != nil {
		return fmt.Errorf("resolve team's manager guids: %w", err)
	}
	if guid, ok := singleGUID(managers); ok {
		if err := q.LinkAppUserYahooManagerSelf(ctx, sqlcdb.LinkAppUserYahooManagerSelfParams{UserID: id, Guid: guid}); err != nil {
			return fmt.Errorf("link picked yahoo account: %w", err)
		}
		return nil
	}
	if err := q.LinkAppUserYahooTeamSelf(ctx, sqlcdb.LinkAppUserYahooTeamSelfParams{
		UserID: id, LeagueID: k.leagueID, TeamID: k.teamID,
	}); err != nil {
		return fmt.Errorf("link picked team: %w", err)
	}
	return nil
}

// rejectTeams records the user's deselection of teams.
func rejectTeams(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID, teams []teamKey) error {
	if len(teams) == 0 {
		return nil
	}
	leagueIDs, teamIDs := splitKeys(teams)
	if err := q.RejectAppUserYahooTeams(ctx, sqlcdb.RejectAppUserYahooTeamsParams{
		UserID: id, LeagueIds: leagueIDs, TeamIds: teamIDs,
	}); err != nil {
		return fmt.Errorf("record team deselections: %w", err)
	}
	return nil
}

// clearRejections removes the user's deselection of teams.
func clearRejections(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID, teams []teamKey) error {
	if len(teams) == 0 {
		return nil
	}
	leagueIDs, teamIDs := splitKeys(teams)
	if _, err := q.ClearAppUserYahooTeamRejections(ctx, sqlcdb.ClearAppUserYahooTeamRejectionsParams{
		UserID: id, LeagueIds: leagueIDs, TeamIds: teamIDs,
	}); err != nil {
		return fmt.Errorf("clear team deselections: %w", err)
	}
	return nil
}

// splitKeys returns the league and team IDs of keys, index for index.
func splitKeys(keys []teamKey) (leagueIDs, teamIDs []int32) {
	leagueIDs, teamIDs = make([]int32, len(keys)), make([]int32, len(keys))
	for i, k := range keys {
		leagueIDs[i], teamIDs[i] = k.leagueID, k.teamID
	}
	return leagueIDs, teamIDs
}

// lockUser takes the user row lock SetTeams takes first (markOfferSeen), so
// Link and Unlink serialize with the user's selections and logins, and
// returns the emails the user's last login synced their email links from:
// the login email and the member Yahoo email (each "" for none), which no
// login can change until the transaction ends. It returns ErrUserNotFound
// for no such user.
func lockUser(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID) ([]string, error) {
	row, err := q.LockAppUserForLinks(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: id %s", ErrUserNotFound, id)
		}
		return nil, fmt.Errorf("lock user: %w", err)
	}
	return []string{row.Email.String, row.MemberYahooEmail.String}, nil
}

// markOfferSeen records that the user saw the team offer.
func markOfferSeen(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID) error {
	n, err := q.MarkAppUserTeamOfferSeen(ctx, id)
	if err != nil {
		return fmt.Errorf("mark team offer seen: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id %s", ErrUserNotFound, id)
	}
	return nil
}

// parseUserID reads an app_users ID in its text form.
func parseUserID(userID string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(userID); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid user id %q: %w", userID, err)
	}
	return id, nil
}
