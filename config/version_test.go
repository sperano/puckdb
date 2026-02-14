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

func TestGetBuildNumberAsInt_ValidNumber(t *testing.T) {
	t.Parallel()
	original := BuildNumber
	t.Cleanup(func() { BuildNumber = original })

	BuildNumber = "123"
	assert.Equal(t, 123, GetBuildNumberAsInt())
}

func TestGetBuildNumberAsInt_InvalidNumber(t *testing.T) {
	t.Parallel()
	original := BuildNumber
	t.Cleanup(func() { BuildNumber = original })

	BuildNumber = BuildNumberNotAvailable
	assert.Equal(t, BuildNumberInvalid, GetBuildNumberAsInt())
}

func TestGetBuildNumberAsInt_EmptyString(t *testing.T) {
	t.Parallel()
	original := BuildNumber
	t.Cleanup(func() { BuildNumber = original })

	BuildNumber = ""
	assert.Equal(t, BuildNumberInvalid, GetBuildNumberAsInt())
}
