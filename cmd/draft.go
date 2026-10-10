package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	leagueIDListSeparator = ","
	// draftOutputFileMode is the permission of a report written with --draft-output.
	draftOutputFileMode = 0o644
)

// draftFlagGroups lists every flag group the draft report commands expose.
var draftFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.DraftFlags,
}

func cmdDraft() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Draft helper reports",
		Long: `Reports the draft helper builds on: league rules (rules), the draftable
player pool (pool) and the stored league rankings (rankings). They read what
the sync imported and computed; run a sync first.`,
	}
	cmd.AddCommand(cmdDraftRules(), cmdDraftPool(), cmdDraftRankings(), cmdDraftSession())
	return cmd
}

func newDraftReportCommand(use, short, long string, run func(context.Context, draftReportRequest) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    long,
		PreRunE: bindFlagsPreRunE(draftFlagGroups...),
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := draftRequestFromFlags()
			if err != nil {
				return err
			}
			return writeDraftReport(cmd.OutOrStdout(), request.Output, func(w io.Writer) error {
				request.Out = w
				return run(cmd.Context(), request)
			})
		},
	}
	config.InitFlags(cmd.Flags(), draftFlagGroups...)
	return cmd
}

func cmdDraftRules() *cobra.Command {
	return newDraftReportCommand("rules", "Compare the leagues' imported scoring and roster rules",
		`Print a Markdown comparison of the leagues' latest imported rules: scoring
categories (direction, points weights, display-only stats), roster slots
(flex, bench and reserve), draft settings, the user's team and every other
setting Yahoo reports, followed by warnings for temporary stand-in, stale,
incomplete or unsupported rules. Use --draft-output to keep it in a file.`,
		runDraftRules)
}

func cmdDraftPool() *cobra.Command {
	return newDraftReportCommand("pool", "Report the coverage of the leagues' draftable player pools",
		`Print, per league, how many players Yahoo lists, how many are eligible at
more than one position, and which players have no Yahoo eligibility or no NHL
player mapping. Such players stay in the pool; the report makes them visible.`,
		runDraftPool)
}

// draftReportRequest is what the draft report commands were asked for.
type draftReportRequest struct {
	Season     int
	LeagueIDs  []int
	Output     string
	StaleAfter time.Duration
	Out        io.Writer
}

func draftRequestFromFlags() (draftReportRequest, error) {
	season := viper.GetInt(config.FlagDraftSeason)
	if season <= 0 {
		return draftReportRequest{}, fmt.Errorf("--%s is required", config.FlagDraftSeason)
	}
	leagueIDs, err := parseLeagueIDs(viper.GetString(config.FlagDraftLeagues))
	if err != nil {
		return draftReportRequest{}, err
	}
	staleAfter := viper.GetInt(config.FlagDraftStaleAfter)
	if staleAfter <= 0 {
		return draftReportRequest{}, fmt.Errorf("--%s must be positive, got %d", config.FlagDraftStaleAfter, staleAfter)
	}
	return draftReportRequest{
		Season: season, LeagueIDs: leagueIDs, Output: viper.GetString(config.FlagDraftOutput),
		StaleAfter: time.Duration(staleAfter) * time.Hour,
	}, nil
}

// parseLeagueIDs reads a comma-separated list of positive league IDs.
func parseLeagueIDs(list string) ([]int, error) {
	var ids []int
	for _, field := range strings.Split(list, leagueIDListSeparator) {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		id, err := strconv.Atoi(field)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid league ID %q in --%s", field, config.FlagDraftLeagues)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("--%s is required", config.FlagDraftLeagues)
	}
	return ids, nil
}

// writeDraftReport sends the report to stdout, or to path when set. A file
// is only replaced once the whole report rendered.
func writeDraftReport(stdout io.Writer, path string, render func(io.Writer) error) error {
	if path == "" {
		return render(stdout)
	}
	var b strings.Builder
	if err := render(&b); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(b.String()), draftOutputFileMode); err != nil {
		return fmt.Errorf("write report to %s: %w", path, err)
	}
	_, err := fmt.Fprintf(stdout, "Wrote %s\n", path)
	return err
}

func runDraftRules(ctx context.Context, request draftReportRequest) error {
	pool, err := openPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()
	now := time.Now()
	reports, err := loadLeagueReports(ctx, sqlcdb.New(pool), request, now)
	if err != nil {
		return err
	}
	return draft.WriteComparison(request.Out, reports, draft.ComparisonOptions{GeneratedAt: now, MaxAge: request.StaleAfter})
}

func loadLeagueReports(ctx context.Context, q draft.Queries, request draftReportRequest, now time.Time) ([]draft.LeagueReport, error) {
	reports := make([]draft.LeagueReport, 0, len(request.LeagueIDs))
	for _, leagueID := range request.LeagueIDs {
		report, err := draft.LoadLeagueReport(ctx, q, request.Season, leagueID, now, request.StaleAfter)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func runDraftPool(ctx context.Context, request draftReportRequest) error {
	pool, err := openPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()
	reports, err := loadLeagueReports(ctx, sqlcdb.New(pool), request, time.Now())
	if err != nil {
		return err
	}
	for _, report := range reports {
		if err := draft.WritePoolReport(request.Out, report); err != nil {
			return err
		}
	}
	return nil
}
