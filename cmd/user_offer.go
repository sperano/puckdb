package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// userResetTeamOfferFlagGroups lists the flag groups of user reset-team-offer,
// which resets through the API server's GraphQL admin mutations (the root's
// --api-server-addr) and never opens the database itself.
var userResetTeamOfferFlagGroups = []*config.FlagGroup{
	&config.AdminAuthFlags,
	&config.APIBasicAuthFlags,
	&config.UserTeamLinkFlags,
	&config.UserResetTeamOfferFlags,
}

// errResetTeamOfferTarget is returned when user reset-team-offer gets both
// or neither of a username and --all-users.
var errResetTeamOfferTarget = fmt.Errorf("pass a username or --%s, exactly one of the two", config.FlagAllUsers)

// errEmptyUsername is returned when user reset-team-offer gets an empty
// username, which names no user.
var errEmptyUsername = errors.New("username must not be empty")

// errInstanceRequired is returned when user reset-team-offer gets no
// --instance.
var errInstanceRequired = fmt.Errorf("--%s is required", config.FlagInstance)

// teamOfferResetter resets team offers on the API server (GraphQLClient).
type teamOfferResetter interface {
	ResetTeamOffer(ctx context.Context, instance, username string) (bool, error)
	ResetInstanceTeamOffers(ctx context.Context, instance string) (int, error)
}

func cmdUserResetTeamOffer() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "reset-team-offer [username]",
		Short:   "Offer the Maurice team picker again at next login",
		Long:    `Clear a user's record of having saved or dismissed the Maurice team picker's first-login offer, or every user's of the instance with --all-users, so the client opens the offer again on the user's next visit. The user's team links and deselections stay: the offer opens on the current selection. Runs through the API server's admin GraphQL mutations (--api-server-addr, with --admin-token or --api-user/--api-password), not the database.`,
		Args:    cobra.MaximumNArgs(1),
		PreRunE: bindFlagsPreRunE(userResetTeamOfferFlagGroups...),
		RunE:    runUserResetTeamOffer,
	}
	config.InitFlags(cmd.Flags(), userResetTeamOfferFlagGroups...)
	return cmd
}

func runUserResetTeamOffer(cmd *cobra.Command, args []string) error {
	all := viper.GetBool(config.FlagAllUsers)
	username, err := resetTeamOfferTarget(args, all)
	if err != nil {
		return err
	}
	instance := viper.GetString(config.FlagInstance)
	if instance == "" {
		return errInstanceRequired
	}
	client, err := getGraphQLClient()
	if err != nil {
		return err
	}
	return resetTeamOffers(cmd.Context(), cmd.OutOrStdout(), client, instance, username, all)
}

// resetTeamOffers resets the team offer of every user of instance with all,
// else username's, and reports the outcome to w.
func resetTeamOffers(ctx context.Context, w io.Writer, api teamOfferResetter, instance, username string, all bool) error {
	if all {
		return resetInstanceTeamOffers(ctx, w, api, instance)
	}
	return resetTeamOffer(ctx, w, api, instance, username)
}

// resetTeamOfferTarget checks that user reset-team-offer gets exactly one
// of a username in args and all, and returns the username, "" with all.
func resetTeamOfferTarget(args []string, all bool) (string, error) {
	if all == (len(args) == 1) {
		return "", errResetTeamOfferTarget
	}
	if all {
		return "", nil
	}
	if args[0] == "" {
		return "", errEmptyUsername
	}
	return args[0], nil
}

// resetInstanceTeamOffers resets the team offer of every user of instance
// and reports how many to w.
func resetInstanceTeamOffers(ctx context.Context, w io.Writer, api teamOfferResetter, instance string) error {
	n, err := api.ResetInstanceTeamOffers(ctx, instance)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Reset the team offer of %d user(s) on instance %q.\n", n, instance)
	return err
}

// resetTeamOffer resets username's team offer on instance and reports the
// outcome to w.
func resetTeamOffer(ctx context.Context, w io.Writer, api teamOfferResetter, instance, username string) error {
	wasSeen, err := api.ResetTeamOffer(ctx, instance, username)
	if err != nil {
		return err
	}
	if !wasSeen {
		_, err = fmt.Fprintf(w, "%s has not seen the team offer yet; it opens at their next visit.\n", username)
		return err
	}
	_, err = fmt.Fprintf(w, "Reset %s's team offer; it opens again at their next visit.\n", username)
	return err
}
