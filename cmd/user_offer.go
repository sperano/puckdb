package cmd

import (
	"context"
	"errors"
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
var errResetTeamOfferTarget = fmt.Errorf("pass a username or --%s, exactly one of the two", config.FlagAllUsers)

// errEmptyUsername is returned when user reset-team-offer gets an empty
// username, which names no user.
var errEmptyUsername = errors.New("username must not be empty")

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
	all := viper.GetBool(config.FlagAllUsers)
	username, err := resetTeamOfferTarget(args, all)
	if err != nil {
		return err
	}
	pool, store, err := openUserTeamLinkStore(cmd.Context())
	if err != nil {
		return err
	}
	defer pool.Close()
	return resetTeamOffers(cmd.Context(), cmd.OutOrStdout(), store, viper.GetString(config.FlagInstance), username, all)
}

// resetTeamOffers resets the team offer of every user of instance with all,
// else username's, and reports the outcome to w.
func resetTeamOffers(ctx context.Context, w io.Writer, store *yahoolink.PGStore, instance, username string, all bool) error {
	if all {
		return resetInstanceTeamOffers(ctx, w, store, instance)
	}
	return resetTeamOffer(ctx, w, store, instance, username)
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
func resetInstanceTeamOffers(ctx context.Context, w io.Writer, store *yahoolink.PGStore, instance string) error {
	n, err := store.ResetInstanceTeamOffers(ctx, instance)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Reset the team offer of %d user(s) on instance %q.\n", n, instance)
	return err
}

// resetTeamOffer resets username's team offer on instance and reports the
// outcome to w.
func resetTeamOffer(ctx context.Context, w io.Writer, store *yahoolink.PGStore, instance, username string) error {
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
