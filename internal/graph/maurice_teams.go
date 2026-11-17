package graph

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/rs/zerolog/log"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/yahoolink"
)

// MauriceTeamPicker lets the current user pick their own Yahoo teams
// (yahoolink.Picker in production). userID is the appuser.User ID.
type MauriceTeamPicker interface {
	TeamChoices(ctx context.Context, userID string) (yahoolink.TeamChoices, error)
	SetTeams(ctx context.Context, userID string, teams []yahoolink.TeamRef) (yahoolink.TeamChoices, error)
	DismissTeamOffer(ctx context.Context, userID string) error
}

// errTeamPickerNotConfigured is returned by the team picker fields when the
// resolver has no MauriceTeams (no database).
var errTeamPickerNotConfigured = errors.New("maurice team picker is not configured")

// errInvalidTeamRef rejects a TeamRef ID that is not a positive 32-bit
// integer, before it could wrap into another team's ID.
var errInvalidTeamRef = errors.New("invalid team reference")

// linkSourceToGQL maps a yahoolink link source to its GraphQL value.
var linkSourceToGQL = map[string]model.MauriceTeamLinkSource{
	yahoolink.SourceEmail:  model.MauriceTeamLinkSourceEmail,
	yahoolink.SourceManual: model.MauriceTeamLinkSourceManual,
	yahoolink.SourceSelf:   model.MauriceTeamLinkSourceSelf,
}

// scopedTeamPicker returns the team picker and the ID of the PuckDB user
// behind the request; every team picker operation is scoped by that user.
func (r *Resolver) scopedTeamPicker(ctx context.Context) (MauriceTeamPicker, string, error) {
	if r.MauriceTeams == nil {
		return nil, "", errTeamPickerNotConfigured
	}
	user, err := appuser.Current(ctx)
	if err != nil {
		return nil, "", err
	}
	return r.MauriceTeams, user.ID, nil
}

func (r *Resolver) mauriceTeamChoices(ctx context.Context) (*model.MauriceTeamChoices, error) {
	picker, userID, err := r.scopedTeamPicker(ctx)
	if err != nil {
		return nil, err
	}
	choices, err := picker.TeamChoices(ctx, userID)
	if err != nil {
		return nil, clientTeamPickerError("mauriceTeamChoices", err)
	}
	return teamChoicesToGQL(choices), nil
}

func (r *Resolver) mauriceSetTeams(ctx context.Context, teams []*model.TeamRef) (*model.MauriceTeamChoices, error) {
	picker, userID, err := r.scopedTeamPicker(ctx)
	if err != nil {
		return nil, err
	}
	refs, err := teamRefsFromGQL(teams)
	if err != nil {
		return nil, err
	}
	choices, err := picker.SetTeams(ctx, userID, refs)
	if err != nil {
		return nil, clientTeamPickerError("mauriceSetTeams", err)
	}
	return teamChoicesToGQL(choices), nil
}

func (r *Resolver) mauriceDismissTeamOffer(ctx context.Context) (bool, error) {
	picker, userID, err := r.scopedTeamPicker(ctx)
	if err != nil {
		return false, err
	}
	if err := picker.DismissTeamOffer(ctx, userID); err != nil {
		return false, clientTeamPickerError("mauriceDismissTeamOffer", err)
	}
	return true, nil
}

// clientTeamPickerError returns err when the client can act on it (a team
// outside the choices, or the request's own end), and otherwise logs it and
// returns errMauriceInternal, so driver or SQL text never reaches the client.
func clientTeamPickerError(op string, err error) error {
	if errors.Is(err, yahoolink.ErrTeamNotOffered) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	log.Error().Err(err).Str("operation", op).Msg("maurice team picker request failed")
	return errMauriceInternal
}

// teamRefsFromGQL converts the mutation's team references, refusing an ID
// that is not a positive 32-bit integer.
func teamRefsFromGQL(teams []*model.TeamRef) ([]yahoolink.TeamRef, error) {
	refs := make([]yahoolink.TeamRef, len(teams))
	for i, t := range teams {
		leagueID, err := teamRefID("leagueId", t.LeagueID)
		if err != nil {
			return nil, err
		}
		teamID, err := teamRefID("teamId", t.TeamID)
		if err != nil {
			return nil, err
		}
		refs[i] = yahoolink.TeamRef{LeagueID: leagueID, TeamID: teamID}
	}
	return refs, nil
}

// teamRefID checks one TeamRef ID before narrowing it.
func teamRefID(field string, v int) (int32, error) {
	if v < 1 || v > math.MaxInt32 {
		return 0, fmt.Errorf("%w: %s %d is not a positive 32-bit integer", errInvalidTeamRef, field, v)
	}
	return int32(v), nil
}

func teamChoicesToGQL(c yahoolink.TeamChoices) *model.MauriceTeamChoices {
	out := &model.MauriceTeamChoices{Teams: make([]*model.MauriceTeamChoice, len(c.Teams)), Offered: c.Offered}
	if c.Season != 0 {
		season := int(c.Season)
		out.Season = &season
	}
	for i, t := range c.Teams {
		choice := &model.MauriceTeamChoice{
			LeagueID: int(t.LeagueID), LeagueName: t.LeagueName,
			TeamID: int(t.TeamID), TeamName: t.TeamName,
			ManagerNicknames: t.ManagerNicknames, Linked: t.Linked(),
		}
		if source, ok := linkSourceToGQL[t.Source]; ok {
			choice.LinkSource = &source
		}
		out.Teams[i] = choice
	}
	return out
}
