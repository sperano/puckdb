package temporal

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Options{HostPort: "localhost:7233", Namespace: "puckdb"}, DefaultOptions())
	assert.Equal(t, config.DefaultTemporalHostPort, DefaultOptions().HostPort)
	assert.Equal(t, config.DefaultTemporalNamespace, DefaultOptions().Namespace)
}

func TestOptionsValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    Options
		wantErr error
	}{
		{name: "defaults", opts: DefaultOptions()},
		{name: "empty host/port", opts: Options{Namespace: "puckdb"}, wantErr: errEmptyHostPort},
		{name: "empty namespace", opts: Options{HostPort: "temporal:7233"}, wantErr: errEmptyNamespace},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, tt.opts.Validate(), tt.wantErr)
		})
	}
}

func TestClientOptions(t *testing.T) {
	t.Parallel()
	got := ClientOptions(Options{HostPort: "temporal.example:7233", Namespace: "puckdb-test"})

	assert.Equal(t, "temporal.example:7233", got.HostPort)
	assert.Equal(t, "puckdb-test", got.Namespace)
	assert.NotNil(t, got.Logger)
	// The single dial option makes calls wait for the server instead of
	// failing while Temporal starts.
	assert.Len(t, got.ConnectionOptions.DialOptions, 1)
}

func TestNewClient_RejectsInvalidOptionsBeforeDialing(t *testing.T) {
	t.Parallel()
	cl, err := NewClient(Options{HostPort: "localhost:7233"})
	require.ErrorIs(t, err, errEmptyNamespace)
	assert.Nil(t, cl)
}
