package projectionfixtures_test

import (
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/projectionfixtures"
	"github.com/stretchr/testify/require"
)

func TestConfigIsValidWithAgingCurve(t *testing.T) {
	cfg := projectionfixtures.Config()
	require.NoError(t, cfg.Validate())
	require.NotNil(t, cfg.AgingCurve)
}
