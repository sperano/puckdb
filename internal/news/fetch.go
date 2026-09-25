package news

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// UserAgent identifies PuckDB to news sources.
	UserAgent = "puckdb-news/1.0 (+https://github.com/sperano/puckdb)"
	// maxFeedBytes bounds a feed response; anything larger is refused.
	maxFeedBytes = 8 << 20
	// defaultFetchTimeout bounds one request when the caller's client has
	// no timeout.
	defaultFetchTimeout = 30 * time.Second
)

// Validators are the conditional-request values a source returned.
type Validators struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	// BodyHash is the hash of the last body read; a source that ignores
	// conditional requests but serves the same body is not reparsed.
	BodyHash string `json:"bodyHash,omitempty"`
}

// FetchResponse is the outcome of a conditional GET.
type FetchResponse struct {
	// NotModified is true when the source answered 304 or served the same
	// body as last time.
	NotModified bool
	Body        []byte
	Validators  Validators
}

// HTTPError is a non-success response.
type HTTPError struct {
	URL        string
	StatusCode int
	// RetryAfter is the delay the source asked for (429/503), zero if none.
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("GET %s: HTTP %d", e.URL, e.StatusCode)
}

// Retryable reports whether retrying may help: server errors, rate limits
// and request timeouts. Other client errors (404, 403) will not fix
// themselves.
func (e *HTTPError) Retryable() bool {
	return e.StatusCode >= http.StatusInternalServerError ||
		e.StatusCode == http.StatusTooManyRequests || e.StatusCode == http.StatusRequestTimeout
}

// ErrFeedTooLarge means a response exceeded maxFeedBytes.
var ErrFeedTooLarge = errors.New("feed response too large")

// Fetch performs a conditional GET with the validators of the last
// successful fetch.
func Fetch(ctx context.Context, client *http.Client, url string, last Validators) (FetchResponse, error) {
	if client.Timeout == 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultFetchTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FetchResponse{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if last.ETag != "" {
		req.Header.Set("If-None-Match", last.ETag)
	}
	if last.LastModified != "" {
		req.Header.Set("If-Modified-Since", last.LastModified)
	}
	resp, err := client.Do(req)
	if err != nil {
		return FetchResponse{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return FetchResponse{NotModified: true, Validators: last}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return FetchResponse{}, &HTTPError{URL: url, StatusCode: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return FetchResponse{}, fmt.Errorf("read %s: %w", url, err)
	}
	if len(body) > maxFeedBytes {
		return FetchResponse{}, fmt.Errorf("GET %s: %w", url, ErrFeedTooLarge)
	}
	sum := sha256.Sum256(body)
	validators := Validators{
		ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), BodyHash: hex.EncodeToString(sum[:]),
	}
	return FetchResponse{NotModified: validators.BodyHash == last.BodyHash, Body: body, Validators: validators}, nil
}

// retryAfter reads a Retry-After header given in seconds or as a date.
func retryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
