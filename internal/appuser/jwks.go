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
	// jwkKeyTypeRSA, jwkUseSignature: the only key type and use accepted.
	jwkKeyTypeRSA   = "RSA"
	jwkUseSignature = "sig"
)

// errUnknownKey is wrapped in ErrInvalidToken for a kid the key set lacks.
var errUnknownKey = errors.New("unknown signing key")

// keySet caches a JWKS by key id. An unknown key id, or keys older than
// jwksMaxAge, trigger a refetch, at most once per jwksMinRefetchInterval.
type keySet struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

func newKeySet(url string, client *http.Client, now func() time.Time) *keySet {
	if client == nil {
		client = &http.Client{Timeout: jwksFetchTimeout}
	}
	return &keySet{url: url, client: client, now: now}
}

// key returns the key named kid. The lock is held across a fetch, so
// concurrent requests needing one wait for it instead of fetching again.
func (s *keySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, known := s.keys[kid]
	fresh := s.now().Sub(s.fetchedAt) < jwksMaxAge
	if known && fresh {
		return key, nil
	}
	if s.now().Sub(s.lastAttempt) >= jwksMinRefetchInterval {
		if err := s.refresh(ctx); err != nil {
			if !known {
				return nil, err
			}
			log.Warn().Err(err).Str("url", s.url).Msg("refresh Cloudflare Access signing keys; using the cached ones")
		}
		key, known = s.keys[kid]
	}
	if !known {
		return nil, fmt.Errorf("%w: %w %q", ErrInvalidToken, errUnknownKey, kid)
	}
	return key, nil
}

// refresh fetches the key set, replacing the cache on success. The fetch
// does not end with the request that triggered it: other requests wait on
// its result.
func (s *keySet) refresh(ctx context.Context) error {
	s.lastAttempt = s.now()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), jwksFetchTimeout)
	defer cancel()
	keys, err := s.fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch Cloudflare Access signing keys: %w", err)
	}
	s.keys, s.fetchedAt = keys, s.now()
	return nil
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
