package config

import (
	stdlog "log"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ServerErrorLog returns a standard library logger for http.Server.ErrorLog
// that forwards the server's own errors (TLS handshakes, a panic no
// middleware recovered) to the global zerolog logger as warnings instead
// of writing them to stderr.
func ServerErrorLog() *stdlog.Logger {
	return stdlog.New(serverErrorWriter{logger: &log.Logger}, "", 0)
}

// serverErrorWriter points at the logger rather than copying it, so it
// follows a later replacement of log.Logger.
type serverErrorWriter struct {
	logger *zerolog.Logger
}

// Write logs one line of the standard library logger.
func (w serverErrorWriter) Write(p []byte) (int, error) {
	w.logger.Warn().Msg(strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}
