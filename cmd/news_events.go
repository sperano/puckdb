package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// newsEventsFlagGroups lists every flag group of `news events`.
var newsEventsFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.NewsEventsReportFlags,
}

// newsEvalFlagGroups lists every flag group of `news eval`.
var newsEvalFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.MauriceFlags,
	&config.NewsExtractModelFlags,
	&config.NewsEvalFlags,
}

func cmdNewsEvents() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Report validated news events, their evidence and the extractions to review",
		Long: `Print a Markdown report of the news events the LLM extraction validated:
each event's report status, stated duration, dates and move, lifecycle
(active, superseded, retracted, resolved) with its history, and the article
versions and verbatim quotes behind it. Extractions that failed, returned
invalid output or had claims dropped are listed for review first. Events
carry no fantasy impact.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), newsEventsFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeDraftReport(cmd.OutOrStdout(), viper.GetString(config.FlagNewsOutput), func(w io.Writer) error {
				return runNewsEvents(cmd.Context(), w)
			})
		},
	}
	config.InitFlags(cmd.Flags(), newsEventsFlagGroups...)
	return cmd
}

func newsEventsRequestFromFlags(now time.Time) (newsevent.DigestRequest, error) {
	days, limit := viper.GetInt(config.FlagNewsSinceDays), viper.GetInt(config.FlagNewsLimit)
	if days <= 0 || limit <= 0 {
		return newsevent.DigestRequest{}, fmt.Errorf("--%s and --%s must be positive", config.FlagNewsSinceDays, config.FlagNewsLimit)
	}
	return newsevent.DigestRequest{
		Since: now.Add(-time.Duration(days) * hoursPerDay * time.Hour), Limit: limit,
		MaxAttempts: max(viper.GetInt(config.FlagNewsExtractMaxAttempts), 1),
		Player: news.Identity{
			NHLPlayerID:   int64(viper.GetInt(config.FlagNewsPlayerNHLID)),
			YahooPlayerID: viper.GetInt(config.FlagNewsPlayerYahooID),
			Name:          requestedPlayerName,
		},
	}, nil
}

func runNewsEvents(ctx context.Context, w io.Writer) error {
	now := time.Now().UTC()
	request, err := newsEventsRequestFromFlags(now)
	if err != nil {
		return err
	}
	pool, err := openPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()
	digest, err := newsevent.LoadEventDigest(ctx, sqlcdb.New(pool), request, now)
	if err != nil {
		return err
	}
	return newsevent.WriteEventDigest(w, digest)
}

func cmdNewsEval() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Measure news event extraction on the labeled corpus",
		Long: `Run the labeled evaluation corpus through the configured extraction model
(--news-extract-provider, --news-extract-model), validating and reconciling
each synthetic article exactly as the worker does, and print extraction
accuracy, the unsupported-claim rate, review routing, lifecycle accuracy and
injection resistance against the release thresholds. A run on the built-in
corpus is recorded in the database; automatic numeric effects stay off for
an extractor until its latest recorded run passed. The command fails when a
threshold is missed.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), newsEvalFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNewsEval(cmd.Context(), cmd.OutOrStdout())
		},
	}
	config.InitFlags(cmd.Flags(), newsEvalFlagGroups...)
	return cmd
}

func runNewsEval(ctx context.Context, stdout io.Writer) error {
	corpusPath := viper.GetString(config.FlagNewsEvalCorpus)
	corpus, err := newsevent.LoadCorpus(corpusPath)
	if err != nil {
		return err
	}
	x, err := newsExtractorFromFlags()
	if err != nil {
		return err
	}
	metrics, err := newsevent.Evaluate(ctx, x, corpus, newsevent.EvalOptions{
		Window:        time.Duration(viper.GetInt(config.FlagNewsIncidentWindowHours)) * time.Hour,
		MaxInputRunes: viper.GetInt(config.FlagNewsExtractMaxInputChars),
	})
	if err != nil {
		return err
	}
	if corpusPath == "" {
		recordNewsEval(ctx, x.Key(), corpus, metrics)
	}
	err = writeDraftReport(stdout, viper.GetString(config.FlagNewsOutput), func(w io.Writer) error {
		return newsevent.WriteEvaluation(w, x.Key(), corpus.Version, metrics)
	})
	if err != nil {
		return err
	}
	if !metrics.Passed() {
		return fmt.Errorf("news event extraction missed the release thresholds")
	}
	return nil
}

// newsExtractorFromFlags builds the extractor the flags name.
func newsExtractorFromFlags() (newsevent.Extractor, error) {
	name := viper.GetString(config.FlagNewsExtractProvider)
	provider, err := llm.ParseProvider(name)
	if err != nil {
		return newsevent.Extractor{}, err
	}
	effort, err := newsevent.ParseReasoningEffort(viper.GetString(config.FlagNewsExtractReasoningEffort))
	if err != nil {
		return newsevent.Extractor{}, err
	}
	model := viper.GetString(config.FlagNewsExtractModel)
	timeout := time.Duration(viper.GetInt(config.FlagNewsExtractTimeoutSeconds)) * time.Second
	return newsevent.Extractor{
		Client:   newsLLMFactory(configuredLLMProviders())(provider, model, timeout),
		Provider: name, Model: model, ReasoningEffort: effort,
		MaxOutputTokens: viper.GetInt(config.FlagNewsExtractMaxOutputTokens),
	}, nil
}

// recordNewsEval stores the run so the worker's gate sees it. A database
// that cannot be reached only costs the record, not the report.
func recordNewsEval(ctx context.Context, key string, corpus newsevent.Corpus, metrics newsevent.Metrics) {
	pool, err := openPGXPool(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("News extraction evaluation not recorded: database unavailable")
		return
	}
	defer pool.Close()
	if _, err := newsevent.RecordEvaluation(ctx, sqlcdb.New(pool), key, corpus, metrics, time.Now().UTC()); err != nil {
		log.Warn().Err(err).Msg("News extraction evaluation not recorded")
	}
}
