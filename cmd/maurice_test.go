package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/maurice"
)

// The API refuses to start with an unregistered model, so the default must
// always be in the registry.
func TestDefaultMauriceModelIsRegistered(t *testing.T) {
	t.Parallel()
	_, err := maurice.FindModel(mauriceModels, config.DefaultMauriceModel)
	assert.NoError(t, err)
}
