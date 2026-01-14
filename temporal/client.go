package temporal

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/config"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/client"
	zerologadapter "logur.dev/adapter/zerolog"
	"google.golang.org/grpc"
	"logur.dev/logur"
)

func temporalOptions() client.Options {
	logger := logur.LoggerToKV(zerologadapter.New(log.Logger))
	return client.Options{
		HostPort: viper.GetString(config.FlagTemporalHostPort),
		//Namespace: viper.GetString(config.FlagTemporalNamespace), TODO
		Logger: logger,
		ConnectionOptions: client.ConnectionOptions{
			DialOptions: []grpc.DialOption{
				grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
			},
		},
	}
}

func NewClient() (client.Client, error) {
	log.Info().Str("host/port", viper.GetString(config.FlagTemporalHostPort)).
		Str("namespace", viper.GetString(config.FlagTemporalNamespace)).Msg("Initializing Temporal")
	cl, err := client.Dial(temporalOptions())
	if err != nil {
		return nil, fmt.Errorf("unable to create temporal client: %w", err)
	}
	return cl, nil
}
