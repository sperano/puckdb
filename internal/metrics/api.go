package metrics

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// API metrics (HTTP middleware), registered on APIRegistry in metrics.go.
var (
	apiHTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "puckdb_http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds",
		Buckets: httpBuckets,
	}, []string{"api", "method", "status_code"})
)

// HTTPMetricsMiddleware returns middleware that records HTTP request metrics for the API.
func HTTPMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ww := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(ww, r)

		duration := time.Since(start)
		path := strings.TrimSuffix(r.URL.Path, "/")
		if path == "" {
			path = "/"
		}
		apiHTTPRequestDuration.WithLabelValues(path, r.Method, fmt.Sprintf("%d", ww.statusCode)).Observe(duration.Seconds())
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
