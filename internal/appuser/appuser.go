// Package appuser resolves the PuckDB user and application session behind a
// request. Identity comes from the Authentik forward-auth headers, which the
// reverse proxy must set and strip from client requests: PuckDB trusts them
// as is. Authentik tokens, cookies and JWTs are never stored; a PuckDB
// session is an opaque random cookie of which only a keyed hash is kept.
package appuser

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Authentik forward-auth headers.
const (
	HeaderUID      = "X-authentik-uid"
	HeaderUsername = "X-authentik-username"
	HeaderName     = "X-authentik-name"
)

const (
	// ProviderAuthentik is the auth_provider of Authentik identities.
	ProviderAuthentik = "authentik"
	// SessionCookieName carries the opaque PuckDB session token.
	SessionCookieName = "puckdb_session"
	// SessionIdleTimeout ends a session after this long without a resolved
	// request. A request after that opens a new session.
	SessionIdleTimeout = 30 * time.Minute
	// sessionTokenBytes is the entropy of a session token.
	sessionTokenBytes = 32
	// HashKeyBytes is the length of a generated session hash key.
	HashKeyBytes = 32
)

var (
	// ErrUnauthenticated is returned when the request carries no trusted
	// Authentik subject.
	ErrUnauthenticated = errors.New("authentication required: missing " + HeaderUID)
	// ErrUserDisabled is returned for a user whose access was disabled.
	ErrUserDisabled = errors.New("user is disabled")
)

// Identity is the trusted Authentik identity of a request. Subject is the
// stable key; Username and DisplayName are mutable snapshots.
type Identity struct {
	Subject     string
	Username    string
	DisplayName string
}

// IdentityFromHeaders reads the Authentik headers. ok is false when the
// subject is missing.
func IdentityFromHeaders(h http.Header) (Identity, bool) {
	id := Identity{
		Subject:     strings.TrimSpace(h.Get(HeaderUID)),
		Username:    strings.TrimSpace(h.Get(HeaderUsername)),
		DisplayName: strings.TrimSpace(h.Get(HeaderName)),
	}
	return id, id.Subject != ""
}

// User is the resolved user and application session of a request.
type User struct {
	ID        string
	SessionID string
}

// SessionRequest asks the store to resolve a user's session.
type SessionRequest struct {
	Identity Identity
	// PresentedHash is the keyed hash of the request's session cookie, nil
	// without a cookie.
	PresentedHash []byte
	// FreshHash is the keyed hash of a new token, used if a new session is
	// opened.
	FreshHash []byte
}

// Resolution is the store's answer. Created reports that a new session was
// opened under FreshHash, so its token must be sent back as the cookie.
type Resolution struct {
	User    User
	Created bool
}

// Store persists users and sessions.
type Store interface {
	Resolve(ctx context.Context, req SessionRequest) (Resolution, error)
}

// Hasher computes keyed hashes of session tokens.
type Hasher struct {
	key []byte
}

// NewHasher returns a Hasher for key, which must not be empty.
func NewHasher(key []byte) (*Hasher, error) {
	if len(key) == 0 {
		return nil, errors.New("session hash key is empty")
	}
	return &Hasher{key: append([]byte(nil), key...)}, nil
}

// RandomHashKey returns a fresh random key. Hashes made with it do not
// survive a restart: every session cookie then opens a new session.
func RandomHashKey() ([]byte, error) {
	key := make([]byte, HashKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session hash key: %w", err)
	}
	return key, nil
}

// Hash returns HMAC-SHA256(key, token).
func (h *Hasher) Hash(token string) []byte {
	mac := hmac.New(sha256.New, h.key)
	mac.Write([]byte(token))
	return mac.Sum(nil)
}

// newSessionToken returns a random URL-safe token.
func newSessionToken() (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sessionCookie is the browser-session cookie carrying token. The server
// enforces SessionIdleTimeout; the cookie itself has no expiry.
func sessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}
