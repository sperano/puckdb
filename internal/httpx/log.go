package httpx

import (
	"fmt"
	"github.com/go-chi/chi/v5/middleware"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

func ChiLogger(next http.Handler) http.Handler {
	return middleware.RequestLogger(logFormatter{})(next)
}

type logFormatter struct{}

type logEntry struct {
	userAgent string
	method    string
	addr      string
	path      string
}

func (l logFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	le := logEntry{
		userAgent: r.Header.Get("user-agent"),
		method:    r.Method,
		addr:      r.RemoteAddr,
		path:      r.URL.Path,
	}
	return le
}

func (l logEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ any) {
	if strings.HasPrefix(l.path, "/ping") {
		return
	}
	log.Info().Str("ua", l.userAgent).
		Str("addr", l.addr).
		Int("status", status).
		Int("in", bytes).
		Dur("dur", elapsed).
		Msgf("%s %s", l.method, l.path)
}

func (l logEntry) Panic(v any, stack []byte) {
	fmt.Printf("panic: %+v\n%s\n", v, string(stack))
}
