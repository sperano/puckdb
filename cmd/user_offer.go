package cmd

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/yahoolink"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// userResetTeamOfferFlagGroups lists the flag groups of user reset-team-offer.
var userResetTeamOfferFlagGroups = slices.Concat(userTeamLinkFlagGroups, []*config.FlagGroup{&config.UserResetTeamOfferFlags})

// errResetTeamOfferTarget is returned when user reset-team-offer gets both
// or neither of a username and --all-users.
var errResetTeamOfferTarget = fmt.Errorf("name one user or pass --%s", config.FlagAllUsers)

func cmdUserResetTeamOffer() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "reset-team-offer [username]",
		Short:   "Offer the Maurice team picker again at next login",
		Long:    `Clear a user's record of having saved or dismissed the Maurice team picker's first-login offer, or every user's of the instance with --all-users, so the client opens the offer again on the user's next visit. The user's team links and deselections stay: the offer opens on the current selection.`,
		Args:    cobra.MaximumNArgs(1),
		PreRunE: bindFlagsPreRunE(userResetTeamOfferFlagGroups...),
		RunE:    runUserResetTeamOffer,
	}
	config.InitFlags(cmd.Flags(), userResetTeamOfferFlagGroups...)
	return cmd
}

func runUserResetTeamOffer(cmd *cobra.Command, args []string) error {
	username, err := resetTeamOfferTarget(args, viper.GetBool(config.FlagAllUsers))
	if err != nil {
		return err
	}
	pool, store, err := openUserTeamLinkStore(cmd.Context())
	if err != nil {
		return err
	}
	defer pool.Close()
	return resetTeamOffer(cmd.Context(), cmd.OutOrStdout(), store, viper.GetString(config.FlagInstance), username)
}

// resetTeamOfferTarget returns the user reset-team-offer resets: the one
// username in args, or "" for every user with all.
func resetTeamOfferTarget(args []string, all bool) (string, error) {
	if all == (len(args) == 1) {
		return "", errResetTeamOfferTarget
	}
	if all {
		return "", nil
	}
	return args[0], nil
}

// resetTeamOffer resets username's team offer on instance, every user's of
// instance when username is empty, and reports the outcome to w.
func resetTeamOffer(ctx context.Context, w io.Writer, store *yahoolink.PGStore, instance, username string) error {
	if username == "" {
		n, err := store.ResetInstanceTeamOffers(ctx, instance)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "Reset the team offer of %d user(s) on instance %q.\n", n, instance)
		return err
	}
	user, err := findUserOrReport(ctx, store, instance, username)
	if err != nil {
		return err
	}
	wasSeen, err := store.ResetTeamOffer(ctx, user.ID)
	if err != nil {
		return err
	}
	if !wasSeen {
		_, err = fmt.Fprintf(w, "%s has not seen the team offer yet; it opens at their next visit.\n", user.Username)
		return err
	}
	_, err = fmt.Fprintf(w, "Reset %s's team offer; it opens again at their next visit.\n", user.Username)
	return err
}
