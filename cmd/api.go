package cmd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/go-chi/chi/v5"
	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/draftboard"
	"github.com/sperano/puckdb/internal/draftboardui"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/sperano/puckdb/internal/graph/generated"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/maurice"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/vektah/gqlparser/v2/ast"
)

const (
	MaxUploadSize = 1 << 32 // 8GB
	MaxMemory     = 1 << 28 // 256MB
	graphQLPath   = "/graphql"
)

// apiFlagGroups lists every flag group the api command exposes. Defined
// once and shared by InitFlags (registration) and BindFlags (viper binding
// in PreRunE) so the two can never drift out of sync.
var apiFlagGroups = []*config.FlagGroup{
	&config.RedisFlags,
	&config.YahooOAuth2Flags,
	&config.TemporalFlags,
	&config.APIPortFlags,
	&config.TLSFlags,
	&config.MauriceFlags,
	&config.PostgresFlags,
	&config.AdminAuthFlags,
	&config.YahooSeasonsFlags,
	&config.DraftAPIFlags,
	&config.DataPathFlags,
	&config.GobCacheFlags,
	&config.YahooDownloadSleepFlags,
	&config.DraftWatchFlags,
}

func cmdAPI() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "api",
		Short: "Start HTTP/GraphQL server",
		Long:  `Start the HTTP server with GraphQL endpoint and Yahoo OAuth handlers`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), apiFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config.LogFlagValues()

			// Open PostgreSQL pool for data queries
			pool, err := database.OpenPGXPool(cmd.Context())
			if err != nil {
				return fmt.Errorf("open database pool: %w", err)
			}
			defer pool.Close()

			// the oauth2 token for yahoo authentication is cached in redis
			redisClient := cache.NewClient()
			defer func() { _ = redisClient.Close() }()

			// create temporal client for GraphQL resolver
			temporalClient, err := temporal.NewClient()
			if err != nil {
				return err
			}
			defer temporalClient.Close()

			queries := sqlcdb.New(pool)

			draftBoard, err := newDraftBoardService(pool, redisClient)
			if err != nil {
				return err
			}
			defer closeDraftBoard(draftBoard)
			resolver := &graph.Resolver{
				TemporalClient: temporalClient,
				RedisClient:    redisClient,
				Queries:        queries,
				DB:             pool,
				Draft:          newDraftService(pool),
				DraftBoard:     draftBoard,
			}

			// Initialize Maurice AI chat
			mauriceService, cleanup, err := initMaurice(cmd.Context(), pool)
			if err != nil {
				log.Warn().Err(err).Msg("Maurice initialization failed, AI chat will be unavailable")
			} else {
				resolver.MauriceService = mauriceService
				defer cleanup()
			}

			listen := fmt.Sprintf(":%d", viper.GetInt(config.FlagAPIPort))
			log.Info().Msgf("Go to %s/ to authenticate with Yahoo or to access the GraphQL console", viper.GetString(config.FlagPublicURL))
			r := setupAPIRouter(redisClient, resolver)
			srv := &http.Server{Addr: listen, Handler: r}
			return runHTTPServer(cmd.Context(), srv,
				viper.GetBool(config.FlagAPITLSEnabled),
				viper.GetString(config.FlagTLSCertificate),
				viper.GetString(config.FlagTLSKey),
			)
		},
	}
	flags := cmd.Flags()
	config.InitFlags(flags, apiFlagGroups...)
	return cmd
}

const draftBoardStalePollIntervals = 3

func newDraftBoardService(pool *pgxpool.Pool, redisClient *redis.Client) (*draftboard.Service, error) {
	gobCache, err := newGobCache(redisClient)
	if err != nil {
		return nil, fmt.Errorf("create live draft cache: %w", err)
	}
	repository := draftwatch.NewRepository(pool)
	runner := draftwatch.Runner{
		Pool: pool, Repository: repository,
		Source: draftwatch.NewYahooSource(store.NewDefaultStorage(), gobCache, shared.NewYahooDownloader(redisClient)),
	}
	interval := time.Duration(viper.GetInt(config.FlagDraftPollInterval)) * time.Second
	watchOptions := draftwatch.WatchOptions{
		Interval:     interval,
		MaxBackoff:   time.Duration(viper.GetInt(config.FlagDraftMaxBackoff)) * time.Second,
		FinalTimeout: time.Duration(viper.GetInt(config.FlagDraftFinalTimeout)) * time.Second,
	}
	return draftboard.NewPGService(pool, draftrank.NewPGStore(pool), runner,
		func() draftboard.WatchRunner { return runner }, watchOptions,
		draftboard.Options{StaleAfter: interval * draftBoardStalePollIntervals}), nil
}

func closeDraftBoard(service *draftboard.Service) {
	ctx, cancel := context.WithTimeout(context.Background(), apiShutdownTimeout)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		log.Warn().Err(err).Msg("stop live draft watchers")
	}
}

// apiShutdownTimeout bounds graceful shutdown of the API server. Long enough
// for in-flight GraphQL queries and Maurice streaming responses to drain.
const apiShutdownTimeout = 30 * time.Second

// runHTTPServer starts srv (TLS or plain) in a goroutine and triggers a
// graceful shutdown when ctx is canceled. Returns nil on clean shutdown,
// otherwise the underlying ListenAndServe error.
func runHTTPServer(ctx context.Context, srv *http.Server, tlsEnabled bool, cert, key string) error {
	errCh := make(chan error, 1)
	go func() {
		var err error
		if tlsEnabled {
			err = srv.ListenAndServeTLS(cert, key)
		} else {
			err = srv.ListenAndServe()
		}
		if err == http.ErrServerClosed {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), apiShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return <-errCh
	}
}

func setupAPIRouter(redisClient *redis.Client, resolver *graph.Resolver) *chi.Mux {
	r := chi.NewRouter()
	r.Use(metrics.HTTPMetricsMiddleware)
	r.Use(httpx.ChiLogger)
	r.Use(httpx.HeaderContext)

	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	r.Handle("/metrics", metrics.HandlerFor(metrics.APIRegistry))
	r.Get("/", homeHandler)
	r.Mount("/draft", draftboardui.Handler())
	// GraphQL
	r.Route(graphQLPath, func(r chi.Router) {
		r.Get("/", playgroundHandler().ServeHTTP)
		r.Post("/query", graphqlHandler(resolver).ServeHTTP)
	})
	// Yahoo Oauth2
	r.Route("/yahoo", func(r chi.Router) {
		r.Get("/login", httpx.YahooLoginHandler(redisClient))
		r.Get("/authenticated", httpx.YahooAuthenticatedHandler(redisClient))
		r.Get("/landed", httpx.YahooLandedHandler)
	})
	return r
}

func graphqlHandler(resolver *graph.Resolver) http.Handler {
	server := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers:  resolver,
		Directives: generated.DirectiveRoot{Admin: graph.AdminDirective},
	}))
	server.AddTransport(transport.Options{})
	server.AddTransport(transport.GET{})
	server.AddTransport(transport.POST{})
	server.AddTransport(transport.MultipartForm{
		MaxUploadSize: MaxUploadSize,
		MaxMemory:     MaxMemory,
	})
	server.SetQueryCache(lru.New[*ast.QueryDocument](config.DefaultGraphQLQueryCacheSize))
	server.Use(extension.Introspection{})
	server.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](config.DefaultGraphQLAPQCacheSize),
	})
	server.AroundResponses(aroundResponsesLogger)
	return server
}

func playgroundHandler() http.Handler {
	return playground.Handler("GraphQL", graphQLPath+"/query")
}

const homeHTML = `<!DOCTYPE html>
<html>
<head>
    <title>PuckDB</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 600px; margin: 50px auto; padding: 20px; }
    </style>
</head>
<body>
    <h1>PuckDB</h1>
    <ul>
        <li><a href="/yahoo/login">Yahoo Login</a></li>
        <li><a href="/draft/">Maurice live draft board</a></li>
        <li><a href="/graphql">GraphQL Console</a></li>
    </ul>
</body>
</html>`

func homeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(homeHTML))
}

// initMaurice sets up the Maurice AI chat service.
// The pool is shared with the main API.
func initMaurice(_ context.Context, pool *pgxpool.Pool) (maurice.Service, func(), error) {
	cfgPath := viper.GetString(config.FlagMauriceConfig)
	if cfgPath == "" {
		cfgPath = maurice.DefaultConfigPath()
	}
	mcpClient, err := maurice.BuildMCPClient(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("setup MCP: %w", err)
	}

	providerConfigs := llm.NewProviderConfigs(llm.ProviderConfigsInput{
		OllamaBaseURL:   viper.GetString(config.FlagOllamaBaseURL),
		AnthropicAPIKey: viper.GetString(config.FlagAnthropicAPIKey),
		OpenAIAPIKey:    viper.GetString(config.FlagOpenAIAPIKey),
	})

	entry, err := maurice.FindModel(mauriceModels, viper.GetString(config.FlagMauriceModel))
	if err != nil {
		mcpClient.Close()
		return nil, nil, fmt.Errorf("select model: %w", err)
	}

	cfg := providerConfigs[entry.Provider]
	llmClient := llm.NewClientForProvider(entry.Provider, cfg, entry.ID)

	svc := maurice.NewService(
		llmClient,
		mcpClient,
		maurice.NewPgDB(pool),
		viper.GetInt(config.FlagMauriceMaxHistory),
		viper.GetInt(config.FlagMauriceMaxTokens),
		viper.GetInt(config.FlagMauriceMaxToolRounds),
	)

	cleanup := func() {
		if err := svc.Close(); err != nil {
			log.Warn().Err(err).Msg("maurice service close")
		}
		mcpClient.Close()
	}

	log.Info().
		Str("provider", entry.Provider.String()).
		Str("model", entry.ID).
		Msg("Maurice AI chat initialized")

	return svc, cleanup, nil
}

func aroundResponsesLogger(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	req := graphql.GetOperationContext(ctx)
	log.Debug().Str("operation", req.OperationName).Msg("starting graphql operation")
	log.Trace().Msg(req.RawQuery)
	res := next(ctx)
	diff := time.Since(req.Stats.OperationStart)
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			log.Error().Str("path", e.Path.String()).Str("duration", diff.String()).Msg(e.Message)
		}
	} else {
		log.Debug().Str("operation", req.OperationName).Str("duration", diff.String()).Msg("graphql operation successful")
	}
	return res
}
