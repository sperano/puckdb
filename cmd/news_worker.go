package cmd

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	puckdbtemporal "github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/newsfeed"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/workflow"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// ensureNewsSchedule makes the Temporal schedule that starts periodic news
// refreshes match minutes: created or updated when positive, removed when
// zero. A run that would overlap a running refresh is skipped; each run only
// fetches the sources whose own refresh interval has passed.
func ensureNewsSchedule(ctx context.Context, schedules client.ScheduleClient, minutes int) error {
	spec := scheduleSpec{Name: "news refresh", ID: shared.ScheduleIDRefreshNews, Action: newsScheduleAction()}
	if minutes > 0 {
		spec.Spec = &client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: time.Duration(minutes) * time.Minute}}}
	}
	return ensureSchedule(ctx, schedules, spec)
}

func newsScheduleAction() *client.ScheduleWorkflowAction {
	return &client.ScheduleWorkflowAction{
		ID:        shared.WorkflowIDRefreshNews,
		Workflow:  workflow.RefreshNewsWorkflow,
		Args:      []any{&model.RefreshNewsInput{}},
		TaskQueue: puckdbtemporal.QueueTasks,
	}
}

// newsHTTPTimeout bounds one news source request.
const newsHTTPTimeout = 30 * time.Second

// registerNewsActivities registers the player news refresh activities.
func registerNewsActivities(w worker.Worker, pool *pgxpool.Pool, queries *sqlcdb.Queries) {
	newsActivities := &newsfeed.Activities{
		Pool:       pool,
		Queries:    queries,
		HTTPClient: &http.Client{Timeout: newsHTTPTimeout},
		LLM:        newsLLMFactory(configuredLLMProviders()),
	}
	w.RegisterActivity(newsActivities.PlanNewsRefresh)
	w.RegisterActivity(newsActivities.FetchNewsSource)
	w.RegisterActivity(newsActivities.RecordNewsFetchFailure)
	w.RegisterActivity(newsActivities.ProcessNewsVersions)
	w.RegisterActivity(newsActivities.ExtractNewsEvents)
	w.RegisterActivity(newsActivities.PruneNews)
}

// configuredLLMProviders reads the provider keys and URLs shared by every LLM
// feature.
func configuredLLMProviders() map[llm.Provider]llm.ProviderConfig {
	return llm.NewProviderConfigs(llm.ProviderConfigsInput{
		OllamaBaseURL:   viper.GetString(config.FlagOllamaBaseURL),
		AnthropicAPIKey: viper.GetString(config.FlagAnthropicAPIKey),
		OpenAIAPIKey:    viper.GetString(config.FlagOpenAIAPIKey),
	})
}

// newsLLMFactory builds news extraction clients from the worker's provider
// settings; the workflow only names the provider and model.
func newsLLMFactory(providers map[llm.Provider]llm.ProviderConfig) newsfeed.ClientFactory {
	return func(provider llm.Provider, model string, timeout time.Duration) llm.Client {
		return llm.NewClientForProvider(provider, providers[provider], model, llm.WithTimeout(timeout))
	}
}
