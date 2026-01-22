package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/redis"
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
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// IsNonRetryableClientError returns true for 4xx errors that should not be retried.
// 404 is excluded because Yahoo Sports sometimes returns intermittent 404s for valid URLs.
func (e *HTTPError) IsNonRetryableClientError() bool {
	if !e.IsClientError() {
		return false
	}
	// 404 should be retried - Yahoo sometimes returns intermittent 404s
	if e.StatusCode == 404 {
		return false
	}
	// Other 4xx errors (400, 401, 403, etc.) should not be retried
	return true
}

type Client interface {
	Download(url string) ([]byte, error)
}

type GenericClient struct {
	Client   *http.Client
	apiLabel string
}

// UserAgent is used for public requests to avoid being blocked as a bot
const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

func (c *GenericClient) Download(url string) ([]byte, error) {
	log.Info().Str("url", url).Msg("Downloading")
	req, err := http.NewRequest("GET", url, nil)
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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

func NewYahooClient(ctx context.Context, redisClient redis.Client) (Client, error) {
	conf, err := config.OauthConfig()
	if err != nil {
		return nil, err
	}
	token, err := redis.LoadToken(ctx, redisClient)
	if errors.Is(err, redis.NewOAuth2TokenMissingError()) {

	}
	if err != nil {
		return nil, err
	}
	tokenSource := conf.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, err
	}
	if newToken.AccessToken != token.AccessToken {
		if err := redis.SaveToken(ctx, redisClient, newToken); err != nil {
			return nil, err
		}
	}
	return &GenericClient{Client: oauth2.NewClient(ctx, tokenSource), apiLabel: "yahoo"}, nil
}

func DownloadYahoo(ctx context.Context, redisClient redis.Client, url string) ([]byte, error) {
	// this can fail if no oauth2 token is found in redis
	client, err := NewYahooClient(ctx, redisClient)
	if err != nil {
		return nil, err
	}
	data, err := client.Download(url)
	return data, err
}

// DownloadPublic downloads from public pages without OAuth2 authentication.
// Use this for public sports.yahoo.com pages that don't require authentication.
func DownloadPublic(url string) ([]byte, error) {
	client := &GenericClient{Client: &http.Client{}, apiLabel: "public"}
	return client.Download(url)
}
