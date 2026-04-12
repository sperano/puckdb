package cmd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/apollotracing"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/graph"
	"github.com/sperano/puckdb/graph/generated"
	handlers "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/maurice"
	mcppkg "github.com/sperano/puckdb/mcp"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/temporal"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/vektah/gqlparser/v2/ast"
)

const (
	MaxUploadSize = 1 << 32 // 8GB
	MaxMemory     = 1 << 28 // 256MB
	graphQLPath   = "/graphql"
)

func cmdAPI() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "api",
		Short: "Start HTTP/GraphQL server",
		Long:  `Start the HTTP server with GraphQL endpoint and Yahoo OAuth handlers`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(),
				&config.YahooOAuth2Flags,
				&config.RedisFlags,
				&config.TemporalFlags,
				&config.APIPortFlags,
				&config.TLSFlags,
				&config.MauriceFlags,
				&config.PostgresFlags,
			)
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

			resolver := &graph.Resolver{
				TemporalClient: temporalClient,
				RedisClient:    redisClient,
				Queries:        queries,
			}

			// Initialize Maurice if configured
			mauriceService, cleanup, err := initMaurice(cmd.Context(), pool)
			if err != nil {
				log.Warn().Err(err).Msg("Maurice initialization failed, AI chat will be unavailable")
			} else if mauriceService != nil {
				resolver.MauriceService = mauriceService
				defer cleanup()
			}

			listen := fmt.Sprintf(":%d", viper.GetInt(config.FlagAPIPort))
			log.Info().Msgf("Go to %s/ to authenticate with Yahoo or to access the GraphQL console", viper.GetString(config.FlagPublicURL))
			r := setupAPIRouter(redisClient, resolver)
			if viper.GetBool(config.FlagAPITLSEnabled) {
				return http.ListenAndServeTLS(listen, viper.GetString(config.FlagTLSCertificate), viper.GetString(config.FlagTLSKey), r)
			}
			return http.ListenAndServe(listen, r)
		},
	}
	flags := cmd.Flags()
	config.InitFlags(flags,
		&config.RedisFlags,
		&config.YahooOAuth2Flags,
		&config.TemporalFlags,
		&config.APIPortFlags,
		&config.TLSFlags,
		&config.MauriceFlags,
		&config.PostgresFlags,
	)
	return cmd
}

func setupAPIRouter(redisClient cache.Client, resolver *graph.Resolver) *chi.Mux {
	r := chi.NewRouter()
	r.Use(metrics.HTTPMetricsMiddleware)
	r.Use(handlers.ChiLogger)

	// Basic CORS
	// for more ideas, see: https://developer.github.com/v3/#cross-origin-resource-sharing
	r.Use(cors.Handler(cors.Options{
		// AllowedOrigins:   []string{"https://foo.com"}, // Use this to allow specific origin hosts.
		AllowedOrigins: []string{"https://*", "http://*", config.DefaultViteDevServerOrigin},
		// AllowOriginFunc: func(r *http.Request, origin string) bool { return true },
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           config.DefaultCORSMaxAge,
	}))

	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	r.Handle("/metrics", metrics.HandlerFor(metrics.APIRegistry))
	r.Get("/", homeHandler)
	// GraphQL
	r.Route(graphQLPath, func(r chi.Router) {
		r.Get("/", playgroundHandler().ServeHTTP)
		r.Post("/query", graphqlHandler(resolver).ServeHTTP)
	})
	// Yahoo Oauth2
	r.Route("/yahoo", func(r chi.Router) {
		r.Get("/login", handlers.YahooLoginHandler)
		r.Get("/authenticated", handlers.YahooAuthenticatedHandler(redisClient))
		r.Get("/landed", handlers.YahooLandedHandler)
	})
	return r
}

func graphqlHandler(resolver *graph.Resolver) http.Handler {
	server := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	server.AddTransport(transport.Websocket{
		KeepAlivePingInterval: config.DefaultWebsocketKeepAlive,
	})
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
	server.Use(apollotracing.Tracer{})
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
        <li><a href="/graphql">GraphQL Console</a></li>
    </ul>
</body>
</html>`

func homeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(homeHTML))
}

// initMaurice sets up the Maurice AI chat service if configured.
// Returns nil service + nil cleanup if Maurice flags are at defaults (opt-in).
// The pool is shared with the main API — Maurice no longer opens its own.
func initMaurice(_ context.Context, pool *pgxpool.Pool) (maurice.Service, func(), error) {
	baseURL := viper.GetString(config.FlagMauriceBaseURL)
	if baseURL == "" {
		return nil, nil, nil
	}

	// MCP client connects lazily on first use (sessions expire quickly)
	mcpURL := viper.GetString(config.FlagMauriceMCPURL)
	mcpClient := mcppkg.NewClient(mcpURL)

	// Create LLM client
	llmClient := llm.NewClient(
		baseURL,
		viper.GetString(config.FlagMauriceAPIKey),
		viper.GetString(config.FlagMauriceModel),
	)

	// Create service
	svc := maurice.NewService(
		llmClient,
		mcpClient,
		sqlcdb.New(pool),
		viper.GetInt(config.FlagMauriceMaxHistory),
		viper.GetInt(config.FlagMauriceMaxTokens),
	)

	cleanup := func() {
		mcpClient.Close()
	}

	log.Info().
		Str("base_url", baseURL).
		Str("model", viper.GetString(config.FlagMauriceModel)).
		Str("mcp_url", mcpURL).
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
