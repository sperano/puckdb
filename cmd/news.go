package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	// newsSourceListSeparator separates --news-only source IDs.
	newsSourceListSeparator = ","
	hoursPerDay             = 24
	// requestedPlayerName labels a player asked for by ID with no incident
	// on record (the report has no other name for them).
	requestedPlayerName = "Requested player"
)

// newsReportFlagGroups lists every flag group of `news report`.
var newsReportFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.NewsSourcesFlags,
	&config.NewsReportFlags,
}

func cmdNews() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "news",
		Short: "Player news for the draft helper",
		Long: `Reports on the player news PuckDB ingests. Refresh news with
"puckdb sync refresh-news" (add --news-force right before a draft), or let the
worker refresh it periodically with --news-schedule-minutes. With
--news-extract-enabled the worker also extracts validated events with an LLM;
"news events" reports them and "news eval" measures the extractor.`,
	}
	cmd.AddCommand(cmdNewsReport(), cmdNewsEvents(), cmdNewsEval())
	return cmd
}

func cmdNewsReport() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Report news coverage, incident candidates and unattached stories",
		Long: `Print a Markdown report of each news source's coverage (fresh, failing,
stale or missing), the incident candidates reported recently with every
report behind them (independent sources, syndicated copies and revisions, with
publication, update and retrieval times), and story subjects that could not be
attached to one player. With --news-player-nhl-id or --news-player-yahoo-id
the report also covers that player; a player with no incident is never
reported as healthy.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), newsReportFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeDraftReport(cmd.OutOrStdout(), viper.GetString(config.FlagNewsOutput), func(w io.Writer) error {
				return runNewsReport(cmd.Context(), w)
			})
		},
	}
	config.InitFlags(cmd.Flags(), newsReportFlagGroups...)
	return cmd
}

// newsReportRequest is what `news report` was asked for.
type newsReportRequest struct {
	Season  int
	Since   time.Duration
	Limit   int
	Player  news.Identity
	Sources []news.Source
}

func newsReportRequestFromFlags() (newsReportRequest, error) {
	sources, err := news.LoadSources(viper.GetString(config.FlagNewsSourcesFile))
	if err != nil {
		return newsReportRequest{}, err
	}
	enabled, err := news.EnabledSources(sources, nil)
	if err != nil {
		return newsReportRequest{}, err
	}
	days, limit := viper.GetInt(config.FlagNewsSinceDays), viper.GetInt(config.FlagNewsLimit)
	if days <= 0 || limit <= 0 {
		return newsReportRequest{}, fmt.Errorf("--%s and --%s must be positive", config.FlagNewsSinceDays, config.FlagNewsLimit)
	}
	season := viper.GetInt(config.FlagNewsSeason)
	if season <= 0 {
		season = nhl.Current().StartYear()
	}
	return newsReportRequest{
		Season: season, Since: time.Duration(days) * hoursPerDay * time.Hour, Limit: limit, Sources: enabled,
		Player: news.Identity{
			NHLPlayerID:   int64(viper.GetInt(config.FlagNewsPlayerNHLID)),
			YahooPlayerID: viper.GetInt(config.FlagNewsPlayerYahooID),
			Name:          requestedPlayerName,
		},
	}, nil
}

func runNewsReport(ctx context.Context, w io.Writer) error {
	now := time.Now().UTC()
	request, err := newsReportRequestFromFlags()
	if err != nil {
		return err
	}
	pool, err := openPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()
	digest, err := loadNewsDigest(ctx, sqlcdb.New(pool), request, now)
	if err != nil {
		return err
	}
	return news.WriteDigest(w, digest)
}

func loadNewsDigest(ctx context.Context, q news.ReportQueries, request newsReportRequest, now time.Time) (news.Digest, error) {
	states, err := news.LoadFetchStates(ctx, q)
	if err != nil {
		return news.Digest{}, err
	}
	digest := news.Digest{
		GeneratedAt: now, Season: request.Season, Since: now.Add(-request.Since),
		Coverage: news.EvaluateCoverage(request.Sources, states, request.Season, now),
	}
	if digest.Incidents, err = news.LoadRecentIncidents(ctx, q, digest.Since, request.Limit); err != nil {
		return news.Digest{}, err
	}
	if digest.Issues, err = news.LoadMentionIssues(ctx, q, digest.Since, request.Limit); err != nil {
		return news.Digest{}, err
	}
	if request.Player.NHLPlayerID == 0 && request.Player.YahooPlayerID == 0 {
		return digest, nil
	}
	player, err := news.LoadPlayerNews(ctx, q, request.Player, digest.Coverage)
	if err != nil {
		return news.Digest{}, err
	}
	if len(player.Incidents) > 0 {
		player.Player.Name = player.Incidents[0].PlayerName
	}
	digest.Player = &player
	return digest, nil
}

// buildRefreshNewsInput reads the sync flags of the refresh-news step.
func buildRefreshNewsInput() *model.RefreshNewsInput {
	input := &model.RefreshNewsInput{}
	if viper.GetBool(config.FlagNewsForce) {
		force := true
		input.Force = &force
	}
	for _, id := range strings.Split(viper.GetString(config.FlagNewsOnly), newsSourceListSeparator) {
		if id = strings.TrimSpace(id); id != "" {
			input.Sources = append(input.Sources, id)
		}
	}
	return input
}
