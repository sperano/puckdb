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
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/graph"
	"github.com/sperano/puckdb/graph/generated"
	handlers "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/redis"
	"github.com/sperano/puckdb/temporal"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/vektah/gqlparser/v2/ast"
)

const (
	MaxUploadSize = 1 << 32 // 8GB - TODO Should be a flag
	MaxMemory     = 1 << 28 // 256M TODO Should be a flag
	graphQLPath   = "/graphql"
)

func cmdAPI() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "api",
		Short: "Start HTTP/GraphQL server",
		Long:  `Start the HTTP server with GraphQL endpoint and Yahoo OAuth handlers`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := config.BindYahooOAuth2Flags(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath)); err != nil {
				return err
			}
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			if err := config.BindPostgresFlags(flags); err != nil {
				return err
			}
			if err := config.BindTemporalFlags(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagAPIPort, flags.Lookup(config.FlagAPIPort)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagAPITLSEnabled, flags.Lookup(config.FlagAPITLSEnabled)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagTLSCertificate, flags.Lookup(config.FlagTLSCertificate)); err != nil {
				return err
			}
			return viper.BindPFlag(config.FlagTLSKey, flags.Lookup(config.FlagTLSKey))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config.LogFlagValues()

			// the oauth2 token for yahoo authentication is cached in redis
			redisClient := redis.NewClient()
			defer func() { _ = redisClient.Close() }()

			// create temporal client for GraphQL resolver
			temporalClient, err := temporal.NewClient()
			if err != nil {
				return err
			}
			defer temporalClient.Close()

			resolver := &graph.Resolver{
				TemporalClient: temporalClient,
			}

			listen := fmt.Sprintf(":%d", viper.GetInt(config.FlagAPIPort))
			log.Info().Msgf("Go to https://%s:%d/yahoo/login to authenticate with Yahoo", viper.GetString(config.FlagYahooHostname), viper.GetInt(config.FlagAPIPort))
			log.Info().Msgf("Go to https://%s:%d/graphql for the GraphQL console", viper.GetString(config.FlagYahooHostname), viper.GetInt(config.FlagAPIPort))
			r := setupAPIRouter(redisClient, resolver)
			if viper.GetBool(config.FlagAPITLSEnabled) {
				return http.ListenAndServeTLS(listen, viper.GetString(config.FlagTLSCertificate), viper.GetString(config.FlagTLSKey), r)
			}
			return http.ListenAndServe(listen, r)
		},
	}
	flags := cmd.Flags()
	config.InitDataPathFlag(flags)
	config.InitRedisFlags(flags)
	config.InitSeasonsFlag(cmd, flags, false)
	config.InitYahooOAuth2Flags(flags)
	config.InitPostgresFlags(flags)
	config.InitTemporalFlags(flags)
	config.InitAPIPortFlag(flags)
	config.InitAPITLSEnabledFlag(flags)
	config.InitTLSCertificate(flags)
	config.InitTLSKey(flags)
	return cmd
}

func setupAPIRouter(redisClient redis.Client, resolver *graph.Resolver) *chi.Mux {
	r := chi.NewRouter()
	r.Use(metrics.HTTPMetricsMiddleware)
	r.Use(handlers.ChiLogger)

	r.Use(database.Middleware)
	r.Use(database.SQLCMiddleware)
	// Basic CORS
	// for more ideas, see: https://developer.github.com/v3/#cross-origin-resource-sharing
	r.Use(cors.Handler(cors.Options{
		// AllowedOrigins:   []string{"https://foo.com"}, // Use this to allow specific origin hosts.
		AllowedOrigins: []string{"https://*", "http://*", "http://localhost:5173"},
		// AllowOriginFunc: func(r *http.Request, origin string) bool { return true },
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))

	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	r.Handle("/metrics", promhttp.Handler())
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
		KeepAlivePingInterval: 10 * time.Second,
	})
	server.AddTransport(transport.Options{})
	server.AddTransport(transport.GET{})
	server.AddTransport(transport.POST{})
	server.AddTransport(transport.MultipartForm{
		MaxUploadSize: MaxUploadSize,
		MaxMemory:     MaxMemory,
	})
	server.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	server.Use(extension.Introspection{})
	server.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
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

func aroundResponsesLogger(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	req := graphql.GetOperationContext(ctx)
	log.Debug().Str("operation", req.OperationName).Msg("starting graphql operation")
	log.Trace().Msg(req.RawQuery)
	res := next(ctx)
	diff := time.Now().Sub(req.Stats.OperationStart)
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			log.Error().Str("path", e.Path.String()).Str("duration", diff.String()).Msg(e.Message)
		}
	} else {
		log.Debug().Str("operation", req.OperationName).Str("duration", diff.String()).Msg("graphql operation successful")
	}
	return res
}
