package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/yahoolink"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// userTeamLinkFlagGroups lists every flag group the user link-team,
// unlink-team and list-teams commands expose.
var userTeamLinkFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.UserTeamLinkFlags,
}

func cmdUser() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Maurice user administration",
		Long:  `Manage the Yahoo team links behind Maurice's "my team": link-team, unlink-team, list-teams; reset-team-offer opens the team picker's first-login offer again.`,
	}
	cmd.AddCommand(cmdUserLinkTeam(), cmdUserUnlinkTeam(), cmdUserListTeams(), cmdUserResetTeamOffer())
	return cmd
}

func cmdUserLinkTeam() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "link-team <username> <league id> <team id>",
		Short:   "Link a user to a Yahoo team by hand",
		Long:    `Link a user to a Yahoo team, for a user whose Yahoo email differs from their login email. When every manager of the team resolves to one Yahoo account (guid), the account is linked, so the user also gets its teams of every other season, including seasons imported later; otherwise (no manager, a hidden guid with no earlier season to recover it from, a co-managed team) the team alone is linked. Promotes an existing link the email resync or the user's own pick made to manual, and clears the user's deselection of the team (Maurice team picker).`,
		Args:    cobra.ExactArgs(3),
		PreRunE: bindFlagsPreRunE(userTeamLinkFlagGroups...),
		RunE:    runUserLinkTeam,
	}
	config.InitFlags(cmd.Flags(), userTeamLinkFlagGroups...)
	return cmd
}

func runUserLinkTeam(cmd *cobra.Command, args []string) error {
	leagueID, teamID, err := parseLeagueTeamArgs(args[1], args[2])
	if err != nil {
		return err
	}
	pool, store, err := openUserTeamLinkStore(cmd.Context())
	if err != nil {
		return err
	}
	defer pool.Close()

	ctx := cmd.Context()
	user, err := findUserOrReport(ctx, store, viper.GetString(config.FlagInstance), args[0])
	if err != nil {
		return err
	}
	link, err := store.Link(ctx, user.ID, leagueID, teamID)
	if err != nil {
		return err
	}
	if link.GUID != "" {
		log.Info().Str("username", user.Username).Int32("league_id", link.LeagueID).Int32("team_id", link.TeamID).
			Str("guid", link.GUID).Msg("linked user to the Yahoo account managing the team")
		return nil
	}
	log.Info().Str("username", user.Username).Int32("league_id", link.LeagueID).Int32("team_id", link.TeamID).
		Msg("linked user to Yahoo team")
	return nil
}

func cmdUserUnlinkTeam() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "unlink-team <username> <league id> <team id>",
		Short:   "Remove a user's link to a Yahoo team",
		Long:    `Remove every link through which a user reaches a Yahoo team, whether made by email match, by hand or by the user's own pick (Maurice team picker): the team link, and the links to the Yahoo accounts (guids) managing it, which also removes those accounts' teams of every other season. Refuses when the login email, or the members file's Yahoo email for it as of the last login, still matches a manager of the team or one of those accounts: fix the Yahoo manager's email or the members file entry instead, since a resync would just recreate the link. Otherwise also clears the user's deselection of the team (Maurice team picker).`,
		Args:    cobra.ExactArgs(3),
		PreRunE: bindFlagsPreRunE(userTeamLinkFlagGroups...),
		RunE:    runUserUnlinkTeam,
	}
	config.InitFlags(cmd.Flags(), userTeamLinkFlagGroups...)
	return cmd
}

func runUserUnlinkTeam(cmd *cobra.Command, args []string) error {
	leagueID, teamID, err := parseLeagueTeamArgs(args[1], args[2])
	if err != nil {
		return err
	}
	pool, store, err := openUserTeamLinkStore(cmd.Context())
	if err != nil {
		return err
	}
	defer pool.Close()

	ctx := cmd.Context()
	user, err := findUserOrReport(ctx, store, viper.GetString(config.FlagInstance), args[0])
	if err != nil {
		return err
	}
	removed, err := store.Unlink(ctx, user.ID, leagueID, teamID)
	if err != nil {
		return err
	}
	if !removed.Removed() {
		return fmt.Errorf("%s has no link to league %d team %d", user.Username, leagueID, teamID)
	}
	log.Info().Str("username", user.Username).Int32("league_id", leagueID).Int32("team_id", teamID).
		Bool("team_link", removed.Team).Strs("guids", removed.GUIDs).Bool("deselection", removed.Rejection).
		Msg("removed user's links to Yahoo team")
	return nil
}

func cmdUserListTeams() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list-teams [username]",
		Short:   "List users' Yahoo team links",
		Long:    `List every Yahoo team link on the instance, or one user's when username is given. A team the user deselected in the team picker is left out.`,
		Args:    cobra.MaximumNArgs(1),
		PreRunE: bindFlagsPreRunE(userTeamLinkFlagGroups...),
		RunE:    runUserListTeams,
	}
	config.InitFlags(cmd.Flags(), userTeamLinkFlagGroups...)
	return cmd
}

func runUserListTeams(cmd *cobra.Command, args []string) error {
	pool, store, err := openUserTeamLinkStore(cmd.Context())
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx := cmd.Context()
	instance := viper.GetString(config.FlagInstance)

	if len(args) == 1 {
		user, err := findUserOrReport(ctx, store, instance, args[0])
		if err != nil {
			return err
		}
		links, err := store.ListUserTeams(ctx, user.ID, nil)
		if err != nil {
			return err
		}
		return printUserTeamLinks(cmd.OutOrStdout(), links, user.Username)
	}

	links, err := store.ListInstanceLinks(ctx, instance)
	if err != nil {
		return err
	}
	return printInstanceTeamLinks(cmd.OutOrStdout(), links)
}

// openUserTeamLinkStore opens the database pool the user commands share and
// checks --instance is set; the caller closes the pool.
func openUserTeamLinkStore(ctx context.Context) (*pgxpool.Pool, *yahoolink.PGStore, error) {
	if viper.GetString(config.FlagInstance) == "" {
		return nil, nil, fmt.Errorf("--%s is required", config.FlagInstance)
	}
	pool, err := openPGXPool(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("open database pool: %w", err)
	}
	return pool, yahoolink.NewPGStore(pool), nil
}

// findUserOrReport resolves username on instance, turning yahoolink's
// lookup errors into a command-line-friendly message.
func findUserOrReport(ctx context.Context, store *yahoolink.PGStore, instance, username string) (yahoolink.User, error) {
	user, err := store.FindUser(ctx, instance, username)
	if errors.Is(err, yahoolink.ErrUserNotFound) || errors.Is(err, yahoolink.ErrAmbiguousUser) {
		return yahoolink.User{}, err
	}
	if err != nil {
		return yahoolink.User{}, fmt.Errorf("find user %q: %w", username, err)
	}
	return user, nil
}

// parseLeagueTeamArgs parses the <league id> <team id> positional arguments
// shared by link-team and unlink-team.
func parseLeagueTeamArgs(leagueArg, teamArg string) (leagueID, teamID int32, err error) {
	l, err := strconv.ParseInt(leagueArg, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid league id %q: %w", leagueArg, err)
	}
	tm, err := strconv.ParseInt(teamArg, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid team id %q: %w", teamArg, err)
	}
	return int32(l), int32(tm), nil
}

func printUserTeamLinks(w io.Writer, links []yahoolink.Link, username string) error {
	if len(links) == 0 {
		_, err := fmt.Fprintf(w, "%s has no linked Yahoo teams.\n", username)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "LEAGUE_ID\tTEAM_ID\tTEAM_NAME\tGUID\tSOURCE\tLINKED_AT")
	for _, l := range links {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\n",
			l.LeagueID, l.TeamID, l.TeamName, linkGUID(l), l.Source, l.LinkedAt.Format("2006-01-02"))
	}
	return tw.Flush()
}

func printInstanceTeamLinks(w io.Writer, links []yahoolink.InstanceLink) error {
	if len(links) == 0 {
		_, err := fmt.Fprintln(w, "No Yahoo team links on this instance.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "USERNAME\tLEAGUE_ID\tTEAM_ID\tTEAM_NAME\tGUID\tSOURCE\tLINKED_AT")
	for _, l := range links {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\t%s\n",
			l.Username, l.Link.LeagueID, l.Link.TeamID, l.Link.TeamName, linkGUID(l.Link), l.Link.Source,
			l.Link.LinkedAt.Format("2006-01-02"))
	}
	return tw.Flush()
}

// noGUID fills the GUID column of a team reached through a team link.
const noGUID = "-"

// linkGUID is the GUID column of l: the Yahoo account it comes through, or
// noGUID for a team link.
func linkGUID(l yahoolink.Link) string {
	if l.GUID == "" {
		return noGUID
	}
	return l.GUID
}
