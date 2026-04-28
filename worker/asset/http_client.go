package asset

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/sperano/puckdb/httpx"
	"github.com/sperano/puckdb/worker/shared"
)

// Transport tuning constants for the asset CDN HTTP client.
// Asset traffic targets public CDNs (assets.nhle.com, s.yimg.com) with no auth.
const (
	assetMaxIdleConns        = 100
	assetMaxIdleConnsPerHost = 16
	assetIdleConnTimeout     = 90 * time.Second
	assetClientTimeout       = 60 * time.Second // overall per-request timeout
)

// assetAPILabel is the api label used for HTTP metrics emitted by the asset downloader.
const assetAPILabel = "asset-cdn"

// NewDownloader constructs a shared.Downloader backed by a single *http.Client
// with a tuned transport. The client is constructed once and shared across all
// asset download calls; it must not be used for authenticated requests.
func NewDownloader() shared.Downloader {
	transport := &http.Transport{
		MaxIdleConns:        assetMaxIdleConns,
		MaxIdleConnsPerHost: assetMaxIdleConnsPerHost,
		IdleConnTimeout:     assetIdleConnTimeout,
		// Disable HTTP/2 ALPN: the asset CDNs (assets.nhle.com / s.yimg.com)
		// emit DATA frames after END_STREAM, which floods stderr with
		// "protocol error: received DATA after END_STREAM" from x/net/http2.
		// HTTP/1.1 with the idle-conn pool above is sufficient for static
		// binary fetches and avoids the noise. A non-nil empty map suppresses
		// h2 negotiation; a nil map keeps the default (h2 enabled).
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	client := &http.Client{
		Timeout:   assetClientTimeout,
		Transport: transport,
	}
	gc := httpx.NewGenericClient(client, assetAPILabel)

	return func(ctx context.Context, url string) ([]byte, error) {
		return gc.Download(ctx, url)
	}
}
