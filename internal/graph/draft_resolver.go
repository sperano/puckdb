package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/workflow"
)

// Draft ranking resolvers. Every read goes through the shared
// draftrank.Service, the same one the CLI export uses, and only reads
// stored snapshots. Access follows the rest of the API: the Authentik
// forward-auth proxy in front of it authenticates every request, and the
// override author is the authenticated Authentik user.

const (
	// defaultDraftPageSize is the page size when a request sets none.
	defaultDraftPageSize = 50
	// maxDraftPageSize caps a requested page size. Rows carry per-scenario
	// placements, contributions and news evidence, so a whole pool in one
	// response (about 850 rows) exhausts the API pod's memory; clients that
	// want the full board page through it.
	maxDraftPageSize = 200
)

var errDraftNotConfigured = errors.New("draft rankings are not available: the API has no database connection")

func (r *Resolver) draft() (*draftrank.Service, error) {
	if r.Draft == nil {
		return nil, errDraftNotConfigured
	}
	return r.Draft, nil
}

// seasonOrCurrent is the requested season start year, or the current one.
func seasonOrCurrent(season *int) int {
	if season != nil && *season > 0 {
		return *season
	}
	return nhl.Current().StartYear()
}

func (r *Resolver) draftLeagues(ctx context.Context, season *int) ([]*model.DraftLeagueSummary, error) {
	svc, err := r.draft()
	if err != nil {
		return nil, err
	}
	year := seasonOrCurrent(season)
	summaries, err := svc.Leagues(ctx, year, r.configuredLeagueIDs(year))
	if err != nil {
		return nil, err
	}
	out := make([]*model.DraftLeagueSummary, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, gqlSummary(summary))
	}
	return out, nil
}

// configuredLeagueIDs are the season's leagues in seasons.yaml; a league
// configured there but never imported is still listed (as MISSING_RULES).
func (r *Resolver) configuredLeagueIDs(season int) []int {
	if r.YahooSeasons == nil {
		return nil
	}
	seasons, err := r.YahooSeasons()
	if err != nil {
		log.Warn().Err(err).Msg("draftLeagues: seasons config unavailable; listing leagues with imported rules only")
		return nil
	}
	var ids []int
	for _, league := range seasons[season].Leagues {
		ids = append(ids, league.LeagueID)
	}
	return ids
}

func (r *Resolver) draftRankings(ctx context.Context, input model.DraftRankingsInput) (*model.DraftRankingsPage, error) {
	query := draftrank.Query{
		Positions: input.Positions, PlayerKeys: input.PlayerKeys,
		Offset: valueOr(input.Offset, 0), Limit: pageSize(input.Limit),
	}
	if input.Search != nil {
		query.Search = *input.Search
	}
	if input.Sort != nil {
		query.Sort = draftrank.SortField(strings.ToLower(string(*input.Sort)))
	}
	if input.Direction != nil {
		query.Direction = draftrank.SortDirection(strings.ToLower(string(*input.Direction)))
	}
	return r.draftPage(ctx, input.League, input.Season, input.SnapshotID, input.Scenario, query)
}

func (r *Resolver) draftPlayerComparison(ctx context.Context, input model.DraftComparisonInput) (*model.DraftRankingsPage, error) {
	if len(input.PlayerKeys) == 0 {
		return nil, fmt.Errorf("%w: name at least one player key", draftrank.ErrInvalidQuery)
	}
	if len(input.PlayerKeys) > maxDraftPageSize {
		return nil, fmt.Errorf("%w: compare at most %d players", draftrank.ErrInvalidQuery, maxDraftPageSize)
	}
	query := draftrank.Query{PlayerKeys: input.PlayerKeys}
	return r.draftPage(ctx, input.League, input.Season, input.SnapshotID, input.Scenario, query)
}

func (r *Resolver) draftPage(ctx context.Context, league string, season *int, snapshot *string, scenario *model.DraftScenario, query draftrank.Query) (*model.DraftRankingsPage, error) {
	svc, err := r.draft()
	if err != nil {
		return nil, err
	}
	ref, err := draftrank.ParseLeague(league, seasonOrCurrent(season))
	if err != nil {
		return nil, err
	}
	snapshotID := uuid.Nil
	if snapshot != nil && *snapshot != "" {
		if snapshotID, err = uuid.Parse(*snapshot); err != nil {
			return nil, fmt.Errorf("%w: snapshot ID %q is not a UUID", draftrank.ErrInvalidQuery, *snapshot)
		}
	}
	if scenario != nil {
		query.Scenario = draftrank.Scenario(strings.ToLower(string(*scenario)))
	}
	page, err := svc.Rankings(ctx, ref, snapshotID, query)
	if err != nil {
		return nil, err
	}
	return gqlPage(page, svc.Now()), nil
}

// pageSize applies the default and the cap; the page reports the size used.
func pageSize(limit *int) int {
	switch {
	case limit == nil || *limit <= 0:
		return defaultDraftPageSize
	case *limit > maxDraftPageSize:
		return maxDraftPageSize
	}
	return *limit
}

func valueOr[T any](value *T, fallback T) T {
	if value == nil {
		return fallback
	}
	return *value
}

func (r *Resolver) draftOverrides(ctx context.Context, leagueKey, playerKey *string, includeInactive *bool) ([]*model.DraftOverride, error) {
	svc, err := r.draft()
	if err != nil {
		return nil, err
	}
	overrides, err := svc.Overrides(ctx, draftrank.OverrideFilter{
		LeagueKey: valueOr(leagueKey, ""), PlayerKey: valueOr(playerKey, ""), IncludeInactive: valueOr(includeInactive, false),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*model.DraftOverride, 0, len(overrides))
	for _, o := range overrides {
		out = append(out, gqlOverride(o))
	}
	return out, nil
}

// createDraftOverride records the override under the authenticated
// Authentik user.
func (r *Resolver) createDraftOverride(ctx context.Context, input model.DraftOverrideCreateInput) (*model.DraftOverride, error) {
	svc, err := r.draft()
	if err != nil {
		return nil, err
	}
	author := httpx.HeadersFromContext(ctx).Get(headerAuthentikUsername)
	created, err := svc.CreateOverride(ctx, overrideFromInput(input), author)
	if err != nil {
		return nil, err
	}
	return gqlOverride(created), nil
}

func (r *Resolver) resetDraftOverride(ctx context.Context, id, reason string) (bool, error) {
	svc, err := r.draft()
	if err != nil {
		return false, err
	}
	if err := svc.ResetOverride(ctx, id, reason); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Resolver) refreshDraftRankings(ctx context.Context, input *model.RefreshDraftRankingsInput) (bool, error) {
	return r.executeWorkflow(ctx, shared.WorkflowIDRefreshDraftRankings, workflow.RefreshDraftRankingsWorkflow, input)
}
