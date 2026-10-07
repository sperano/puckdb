package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// draftListSeparator separates the values of list flags (positions, player
// keys, league IDs).
const draftListSeparator = ","

// draftRankingsFlagGroups lists every flag group of `draft rankings`.
var draftRankingsFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.DraftRankingsFlags,
}

func cmdDraftRankings() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rankings",
		Short: "Show or export a league's stored draft rankings",
		Long: `Print a league's draft rankings from its latest stored snapshot (or the one
named with --draft-snapshot) as a table, CSV or JSON. This reads the same
snapshots through the same service as the GraphQL API, so the same league,
filters and snapshot give the same values and ranks; it never recomputes a
ranking. Refresh snapshots with "puckdb sync refresh-draft-rankings".

Positions filter with OR semantics (--draft-positions C,LW lists a C/LW
player once) and never renumber overall or position ranks.

Example:
  puckdb draft rankings --draft-league 465.l.1001 --draft-positions C,LW --draft-format csv`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), draftRankingsFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := draftRankingsRequestFromFlags()
			if err != nil {
				return err
			}
			return writeDraftReport(cmd.OutOrStdout(), viper.GetString(config.FlagDraftOutput), func(w io.Writer) error {
				return runDraftRankings(cmd.Context(), w, cmd.ErrOrStderr(), request)
			})
		},
	}
	config.InitFlags(cmd.Flags(), draftRankingsFlagGroups...)
	return cmd
}

// draftRankingsRequest is what `draft rankings` was asked for.
type draftRankingsRequest struct {
	League   draftrank.LeagueRef
	Snapshot uuid.UUID
	Query    draftrank.Query
	Format   draftrank.Format
}

func draftRankingsRequestFromFlags() (draftRankingsRequest, error) {
	season := viper.GetInt(config.FlagDraftSeason)
	if season <= 0 {
		season = nhl.Current().StartYear()
	}
	league, err := draftrank.ParseLeague(viper.GetString(config.FlagDraftLeague), season)
	if err != nil {
		return draftRankingsRequest{}, fmt.Errorf("--%s: %w", config.FlagDraftLeague, err)
	}
	request := draftRankingsRequest{
		League: league,
		Format: draftrank.Format(strings.ToLower(viper.GetString(config.FlagDraftFormat))),
		Query: draftrank.Query{
			Scenario:   draftrank.Scenario(strings.ToLower(viper.GetString(config.FlagDraftScenario))),
			Positions:  splitDraftList(viper.GetString(config.FlagDraftPositions)),
			PlayerKeys: splitDraftList(viper.GetString(config.FlagDraftPlayers)),
			Search:     viper.GetString(config.FlagDraftSearch),
			Sort:       draftrank.SortField(strings.ToLower(viper.GetString(config.FlagDraftSort))),
			Direction:  draftrank.SortDirection(strings.ToLower(viper.GetString(config.FlagDraftDirection))),
			Offset:     viper.GetInt(config.FlagDraftOffset),
			Limit:      viper.GetInt(config.FlagDraftLimit),
		},
	}
	if !slices.Contains(draftrank.Formats, request.Format) {
		return draftRankingsRequest{}, fmt.Errorf("--%s must be one of table, csv, json; got %q", config.FlagDraftFormat, request.Format)
	}
	if id := strings.TrimSpace(viper.GetString(config.FlagDraftSnapshot)); id != "" {
		if request.Snapshot, err = uuid.Parse(id); err != nil {
			return draftRankingsRequest{}, fmt.Errorf("--%s %q is not a snapshot ID", config.FlagDraftSnapshot, id)
		}
	}
	return request, nil
}

func splitDraftList(list string) []string {
	var values []string
	for _, value := range strings.Split(list, draftListSeparator) {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func runDraftRankings(ctx context.Context, w, stderr io.Writer, request draftRankingsRequest) error {
	pool, err := openPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()
	return exportDraftRankings(ctx, w, stderr, newDraftService(pool), request)
}

// errNoRanking means the league has no snapshot to export.
var errNoRanking = errors.New("no ranking available")

// exportDraftRankings reads one page through the shared service and writes
// it in the requested format. CSV rows cannot carry the page's status and
// issues, so they go to stderr; a league without a snapshot is an error in
// every format, so a scripted export never succeeds with an empty file.
func exportDraftRankings(ctx context.Context, w, stderr io.Writer, service *draftrank.Service, request draftRankingsRequest) error {
	page, err := service.Rankings(ctx, request.League, request.Snapshot, request.Query)
	if err != nil {
		return err
	}
	if err := draftrank.Export(w, page, request.Format); err != nil {
		return err
	}
	if request.Format == draftrank.FormatCSV {
		reportDraftIssues(stderr, page)
	}
	if page.Status != draftrank.StatusReady {
		codes := make([]string, 0, len(page.Issues))
		for _, issue := range page.Issues {
			codes = append(codes, string(issue.Code))
		}
		return fmt.Errorf("%w: league status %s (%s)", errNoRanking, page.Status, strings.Join(codes, ", "))
	}
	return nil
}

func reportDraftIssues(stderr io.Writer, page draftrank.Page) {
	fmt.Fprintf(stderr, "status %s\n", page.Status)
	for _, issue := range page.Issues {
		fmt.Fprintf(stderr, "%s: %s\n", issue.Code, issue.Message)
	}
}

// buildRefreshDraftRankingsInput reads the sync flags of the
// refresh-draft-rankings step.
func buildRefreshDraftRankingsInput() (*model.RefreshDraftRankingsInput, error) {
	input := &model.RefreshDraftRankingsInput{}
	leagueIDs, err := parseLeagueIDs(viper.GetString(config.FlagDraftLeagues))
	if err == nil {
		input.LeagueIds = leagueIDs
	} else if strings.TrimSpace(viper.GetString(config.FlagDraftLeagues)) != "" {
		return nil, err
	}
	switch bench := strings.ToUpper(strings.TrimSpace(viper.GetString(config.FlagDraftBenchPolicy))); model.DraftBenchPolicy(bench) {
	case "":
	case model.DraftBenchPolicyIncluded, model.DraftBenchPolicyExcluded:
		input.BenchPolicy = new(model.DraftBenchPolicy(bench))
	default:
		return nil, fmt.Errorf("--%s must be included or excluded, got %q", config.FlagDraftBenchPolicy, bench)
	}
	if viper.GetBool(config.FlagDraftWorkloadCaps) {
		input.WorkloadCapPolicy = new(model.DraftWorkloadCapPolicyPerPlayer)
	}
	if raw := strings.TrimSpace(viper.GetString(config.FlagDraftUncertaintyPenalty)); raw != "" {
		penalty, err := strconv.ParseFloat(raw, 64)
		if err != nil || penalty < 0 || math.IsInf(penalty, 0) || math.IsNaN(penalty) {
			return nil, fmt.Errorf("--%s must be a nonnegative number, got %q", config.FlagDraftUncertaintyPenalty, raw)
		}
		input.UncertaintyPenalty = &penalty
	}
	return input, nil
}
