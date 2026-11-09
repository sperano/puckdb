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
		Long:  `Manage the Yahoo team links behind Maurice's "my team": link-team, unlink-team, list-teams.`,
	}
	cmd.AddCommand(cmdUserLinkTeam(), cmdUserUnlinkTeam(), cmdUserListTeams())
	return cmd
}

func cmdUserLinkTeam() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "link-team <username> <league id> <team id>",
		Short:   "Link a user to a Yahoo team by hand",
		Long:    `Link a user to a Yahoo team, for a user whose Yahoo email differs from their login email. Promotes an existing email-sourced link for the same team to manual, so a later email resync never removes it.`,
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
	user, err := findUserOrReport(ctx, store, args[0])
	if err != nil {
		return err
	}
	link, err := store.Link(ctx, user.ID, leagueID, teamID)
	if err != nil {
		return err
	}
	log.Info().Str("username", user.Username).Int32("league_id", link.LeagueID).Int32("team_id", link.TeamID).
		Msg("linked user to Yahoo team")
	return nil
}

func cmdUserUnlinkTeam() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "unlink-team <username> <league id> <team id>",
		Short:   "Remove a user's link to a Yahoo team",
		Long:    `Remove a user's link to a Yahoo team, whether it was made by email match or by hand. Refuses an email-sourced link whose login email still matches it: fix the Yahoo manager's email instead, since a resync would just recreate the link.`,
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
	user, err := findUserOrReport(ctx, store, args[0])
	if err != nil {
		return err
	}
	removed, err := store.Unlink(ctx, user.ID, user.Email, leagueID, teamID)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("%s has no link to league %d team %d", user.Username, leagueID, teamID)
	}
	log.Info().Str("username", user.Username).Int32("league_id", leagueID).Int32("team_id", teamID).
		Msg("removed user's link to Yahoo team")
	return nil
}

func cmdUserListTeams() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list-teams [username]",
		Short:   "List users' Yahoo team links",
		Long:    `List every Yahoo team link on the instance, or one user's when username is given.`,
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
		user, err := findUserOrReport(ctx, store, args[0])
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

// findUserOrReport resolves username on the configured instance, turning
// yahoolink's lookup errors into a command-line-friendly message.
func findUserOrReport(ctx context.Context, store *yahoolink.PGStore, username string) (yahoolink.User, error) {
	instance := viper.GetString(config.FlagInstance)
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
	fmt.Fprintln(tw, "LEAGUE_ID\tTEAM_ID\tTEAM_NAME\tSOURCE\tLINKED_AT")
	for _, l := range links {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\n", l.LeagueID, l.TeamID, l.TeamName, l.Source, l.LinkedAt.Format("2006-01-02"))
	}
	return tw.Flush()
}

func printInstanceTeamLinks(w io.Writer, links []yahoolink.InstanceLink) error {
	if len(links) == 0 {
		_, err := fmt.Fprintln(w, "No Yahoo team links on this instance.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "USERNAME\tLEAGUE_ID\tTEAM_ID\tTEAM_NAME\tSOURCE\tLINKED_AT")
	for _, l := range links {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\n",
			l.Username, l.Link.LeagueID, l.Link.TeamID, l.Link.TeamName, l.Link.Source, l.Link.LinkedAt.Format("2006-01-02"))
	}
	return tw.Flush()
}
