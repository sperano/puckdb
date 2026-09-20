package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

// HTTPError represents an HTTP error with status code
type HTTPError struct {
	StatusCode int
	Status     string
	URL        string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("download failed: %s (url: %s)", e.Status, e.URL)
}

// IsClientError returns true for 4xx status codes
func (e *HTTPError) IsClientError() bool {
	return e.StatusCode >= http.StatusBadRequest && e.StatusCode < http.StatusInternalServerError
}

// IsNonRetryableClientError returns true for 4xx errors that should not be retried.
// 404 is excluded because Yahoo Sports sometimes returns intermittent 404s for valid URLs.
func (e *HTTPError) IsNonRetryableClientError() bool {
	if !e.IsClientError() {
		return false
	}
	// 404 should be retried - Yahoo sometimes returns intermittent 404s
	if e.StatusCode == http.StatusNotFound {
		return false
	}
	// Other 4xx errors (400, 401, 403, etc.) should not be retried
	return true
}

type Client interface {
	Download(ctx context.Context, url string) ([]byte, error)
}

type GenericClient struct {
	Client   *http.Client
	apiLabel string
}

// UserAgent is used for public requests to avoid being blocked as a bot
const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// publicHTTPClient is shared across DownloadPublic calls so TCP connections
// to public Yahoo pages are kept alive and reused instead of being
// re-established (and re-TLS-handshaked) on every download.
var publicHTTPClient = &http.Client{Timeout: config.DefaultHTTPClientTimeout}

func (c *GenericClient) Download(ctx context.Context, url string) ([]byte, error) {
	log.Trace().Str("url", url).Msg("Downloading")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)

	start := time.Now()
	resp, err := c.Client.Do(req)
	if err != nil {
		metrics.ObserveHTTP(c.apiLabel, req.Method, 0, time.Since(start), 0)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP(c.apiLabel, req.Method, resp.StatusCode, duration, 0)
		return nil, err
	}

	metrics.ObserveHTTP(c.apiLabel, req.Method, resp.StatusCode, duration, len(body))

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		log.Error().
			Str("url", url).
			Int("status_code", resp.StatusCode).
			Str("status", resp.Status).
			Str("body", string(body)).
			Msg("Download failed")
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			URL:        url,
		}
	}

	log.Trace().Msgf("body downloaded:\n%s", string(body))
	return body, nil
}

func NewYahooClient(ctx context.Context, redisClient *redis.Client) (Client, error) {
	conf, err := config.OauthConfig()
	if err != nil {
		return nil, err
	}
	return NewYahooClientWithConfig(ctx, redisClient, conf)
}

// NewYahooClientWithConfig builds an authenticated Yahoo client for the user
// in ctx. The token is validated (and refreshed, if expired) up front so a
// user who must log in again is told so here, not by a failed download.
func NewYahooClientWithConfig(ctx context.Context, redisClient *redis.Client, conf *oauth2.Config) (Client, error) {
	user, err := config.UserFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating yahoo client: %w", err)
	}
	tokenSource, err := newRefreshingTokenSource(ctx, redisClient, conf, user)
	if err != nil {
		return nil, err
	}
	if _, err := tokenSource.Token(); err != nil {
		return nil, err
	}
	return &GenericClient{Client: oauth2.NewClient(ctx, tokenSource), apiLabel: "yahoo"}, nil
}

func DownloadYahoo(ctx context.Context, redisClient *redis.Client, url string) ([]byte, error) {
	// this can fail if no oauth2 token is found in redis
	client, err := NewYahooClient(ctx, redisClient)
	if err != nil {
		// Preserve the matched token error's context; only fill in the login
		// URL when it doesn't already carry one.
		if tokenErr, ok := errors.AsType[*cache.OAuth2TokenMissingError](err); ok {
			if tokenErr.PublicURL == "" {
				tokenErr.PublicURL = viper.GetString(config.FlagPublicURL)
			}
			return nil, fmt.Errorf("%s: %w", url, tokenErr)
		}
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	data, err := client.Download(ctx, url)
	return data, err
}

// DownloadPublic downloads from public pages without OAuth2 authentication.
// Use this for public sports.yahoo.com pages that don't require authentication.
func DownloadPublic(ctx context.Context, url string) ([]byte, error) {
	client := NewGenericClient(publicHTTPClient, "public")
	return client.Download(ctx, url)
}

// NewGenericClient constructs a GenericClient with the given underlying HTTP
// client and api label. The label is used for HTTP metric emission so callers
// from different packages can be distinguished in dashboards.
func NewGenericClient(client *http.Client, apiLabel string) *GenericClient {
	return &GenericClient{Client: client, apiLabel: apiLabel}
}
