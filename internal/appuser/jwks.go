package appuser

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// jwksFetchTimeout bounds one fetch of the signing keys.
	jwksFetchTimeout = 10 * time.Second
	// jwksMaxAge is how long fetched keys are used before a refetch, so a
	// key Cloudflare retired stops being accepted.
	jwksMaxAge = time.Hour
	// jwksMinRefetchInterval spaces fetches, so tokens naming unknown keys
	// cannot make every request fetch.
	jwksMinRefetchInterval = time.Minute
	// maxJWKSBytes bounds the key set response.
	maxJWKSBytes = 1 << 20
	// minRSAKeyBits is the smallest accepted signing key.
	minRSAKeyBits = 2048
	// minRSAExponent and maxRSAExponent bound the public exponent (the
	// range crypto/rsa accepts for verification).
	minRSAExponent = 2
	maxRSAExponent = math.MaxInt32
	// jwkKeyTypeRSA is the only key type accepted; a key's use must be
	// absent or jwkUseSignature.
	jwkKeyTypeRSA   = "RSA"
	jwkUseSignature = "sig"
)

// errUnknownKey is wrapped in ErrInvalidToken for a kid the key set lacks.
var errUnknownKey = errors.New("unknown signing key")

// keySet caches a JWKS by key id. An unknown key id, or keys older than
// jwksMaxAge, trigger a refetch, at most once per jwksMinRefetchInterval.
// Cached fresh keys are served without waiting on a fetch in flight.
type keySet struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	// lastErr is the error of the last fetch, nil when it succeeded.
	lastErr error
	// inflight is closed when the running fetch ends; nil when none runs.
	inflight chan struct{}
}

func newKeySet(url string, client *http.Client, now func() time.Time) *keySet {
	if client == nil {
		client = &http.Client{Timeout: jwksFetchTimeout}
	}
	return &keySet{url: url, client: client, now: now}
}

// key returns the key named kid. A request needing a fetch starts one, or
// joins the one in flight, and waits for it until ctx ends. While the last
// fetch failed, a kid the cache lacks returns that fetch error, not
// ErrInvalidToken.
func (s *keySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	for {
		s.mu.Lock()
		key, known := s.keys[kid]
		if known && s.now().Sub(s.fetchedAt) < jwksMaxAge {
			s.mu.Unlock()
			return key, nil
		}
		if s.inflight == nil && s.now().Sub(s.lastAttempt) >= jwksMinRefetchInterval {
			s.startRefresh(ctx)
		}
		if done := s.inflight; done != nil {
			s.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		lastErr := s.lastErr
		s.mu.Unlock()
		switch {
		case known:
			return key, nil
		case lastErr != nil:
			return nil, fmt.Errorf("fetch Cloudflare Access signing keys: %w", lastErr)
		default:
			return nil, fmt.Errorf("%w: %w %q", ErrInvalidToken, errUnknownKey, kid)
		}
	}
}

// startRefresh starts a fetch of the key set; s.mu must be held. The fetch
// does not end with the request that triggered it: other requests wait on
// its result. A failed fetch keeps the cached keys.
func (s *keySet) startRefresh(ctx context.Context) {
	done := make(chan struct{})
	s.inflight, s.lastAttempt = done, s.now()
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), jwksFetchTimeout)
		defer cancel()
		keys, err := s.fetch(ctx)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.inflight, s.lastErr = nil, err
		if err != nil {
			log.Warn().Err(err).Str("url", s.url).Msg("fetch Cloudflare Access signing keys; keeping the cached ones")
			return
		}
		s.keys, s.fetchedAt = keys, s.now()
	}()
}

func (s *keySet) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxJWKSBytes {
		return nil, fmt.Errorf("key set larger than %d bytes", maxJWKSBytes)
	}
	return parseJWKS(body)
}

// jwk is one JSON Web Key; only RSA signing keys are used.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// parseJWKS returns the RS256 signing keys of a JWKS document by key id.
// Keys of another type, use or algorithm are skipped; a malformed RSA key or
// a document with no usable key is an error.
func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode key set: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != jwkKeyTypeRSA || k.Kid == "" ||
			(k.Use != "" && k.Use != jwkUseSignature) || (k.Alg != "" && k.Alg != jwtAlgRS256) {
			continue
		}
		key, err := rsaPublicKey(k)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k.Kid, err)
		}
		keys[k.Kid] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("key set has no RS256 signing key")
	}
	return keys, nil
}

func rsaPublicKey(k jwk) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("modulus: %w", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("exponent: %w", err)
	}
	modulus := new(big.Int).SetBytes(n)
	if modulus.BitLen() < minRSAKeyBits {
		return nil, fmt.Errorf("modulus of %d bits, want at least %d", modulus.BitLen(), minRSAKeyBits)
	}
	exponent := new(big.Int).SetBytes(e)
	if !exponent.IsInt64() || exponent.Int64() < minRSAExponent || exponent.Int64() > maxRSAExponent {
		return nil, errors.New("exponent out of range")
	}
	return &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}, nil
}
