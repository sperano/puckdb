package temporal

import (
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"
)

// Options are the Temporal client settings. The command layer reads them
// from its flags; library code and tests pass them explicitly.
type Options struct {
	HostPort  string
	Namespace string
}

var (
	errEmptyHostPort  = errors.New("temporal host/port is empty")
	errEmptyNamespace = errors.New("temporal namespace is empty")
)

// DefaultOptions returns the settings the temporal flags default to.
func DefaultOptions() Options {
	return Options{
		HostPort:  config.DefaultTemporalHostPort,
		Namespace: config.DefaultTemporalNamespace,
	}
}

// Validate rejects empty settings. An empty namespace would otherwise make
// the SDK fall back to its own "default" namespace instead of failing.
func (o Options) Validate() error {
	if o.HostPort == "" {
		return errEmptyHostPort
	}
	if o.Namespace == "" {
		return errEmptyNamespace
	}
	return nil
}

// ClientOptions maps o to the SDK client options: zerolog logging, and calls
// that wait for the server to become ready instead of failing fast.
func ClientOptions(o Options) client.Options {
	logger := logur.LoggerToKV(zerologadapter.New(log.Logger))
	return client.Options{
		HostPort:  o.HostPort,
		Namespace: o.Namespace,
		Logger:    logger,
		ConnectionOptions: client.ConnectionOptions{
			DialOptions: []grpc.DialOption{
				grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
			},
		},
	}
}

// NewClient validates o and dials Temporal.
func NewClient(o Options) (client.Client, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	log.Info().Str("host/port", o.HostPort).Str("namespace", o.Namespace).Msg("Initializing Temporal")
	cl, err := client.Dial(ClientOptions(o))
	if err != nil {
		return nil, fmt.Errorf("unable to create temporal client: %w", err)
	}
	return cl, nil
}
