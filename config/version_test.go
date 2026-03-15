package config

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func TestLogIntro(t *testing.T) {
	// Not parallel - modifies global logger
	originalLogger := log.Logger
	t.Cleanup(func() {
		log.Logger = originalLogger
	})

	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.InfoLevel)

	LogIntro()

	output := buf.String()
	assert.Contains(t, output, "PuckDB")
	assert.Contains(t, output, "CPUs")
	assert.Contains(t, output, "build")
}
