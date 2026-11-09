package config

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerErrorWriter_LogsOneWarningPerLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	w := serverErrorWriter{logger: &logger}
	const line = "http: TLS handshake error from 192.0.2.1:5000: EOF\n"

	n, err := w.Write([]byte(line))

	require.NoError(t, err)
	assert.Equal(t, len(line), n)
	var logged map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &logged))
	assert.Equal(t, "warn", logged["level"])
	assert.Equal(t, "http: TLS handshake error from 192.0.2.1:5000: EOF", logged["message"])
}
