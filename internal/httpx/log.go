package httpx

import (
	"fmt"
	stdlog "log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ChiLogger logs each request, and each panic middleware.Recoverer
// recovers after it, through the global zerolog logger.
func ChiLogger(next http.Handler) http.Handler {
	return middleware.RequestLogger(logFormatter{logger: &log.Logger})(next)
}

// ServerErrorLog returns a standard library logger for http.Server.ErrorLog
// that forwards the server's own errors (TLS handshakes, a panic no
// middleware recovered) to the global zerolog logger as warnings instead
// of writing them to stderr.
func ServerErrorLog() *stdlog.Logger {
	return stdlog.New(serverErrorWriter{logger: &log.Logger}, "", 0)
}

// logFormatter points at the logger rather than copying it, so the
// request logger follows a later replacement of log.Logger.
type logFormatter struct {
	logger *zerolog.Logger
}

type logEntry struct {
	logger    *zerolog.Logger
	userAgent string
	method    string
	addr      string
	path      string
}

func (l logFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	le := logEntry{
		logger:    l.logger,
		userAgent: r.Header.Get("user-agent"),
		method:    r.Method,
		addr:      r.RemoteAddr,
		path:      r.URL.Path,
	}
	return le
}

// Write logs a finished request; bytes is the response body size.
func (l logEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ any) {
	if strings.HasPrefix(l.path, "/ping") {
		return
	}
	l.logger.Info().Str("ua", l.userAgent).
		Str("addr", l.addr).
		Int("status", status).
		Int("bytes", bytes).
		Dur("dur", elapsed).
		Msgf("%s %s", l.method, l.path)
}

// Panic logs a panic middleware.Recoverer recovered, with the request it
// interrupted and the goroutine stack.
func (l logEntry) Panic(v any, stack []byte) {
	l.logger.Error().Str("ua", l.userAgent).
		Str("addr", l.addr).
		Str("method", l.method).
		Str("path", l.path).
		Str("panic", fmt.Sprintf("%+v", v)).
		Str("stack", string(stack)).
		Msg("Recovered panic in HTTP handler")
}

type serverErrorWriter struct {
	logger *zerolog.Logger
}

// Write logs one line of the standard library logger.
func (w serverErrorWriter) Write(p []byte) (int, error) {
	w.logger.Warn().Msg(strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}
