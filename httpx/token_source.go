package httpx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"golang.org/x/oauth2"
)

// oauthErrorInvalidGrant is RFC 6749's error code for a refresh token the
// provider no longer accepts (revoked, expired, or already rotated away).
const oauthErrorInvalidGrant = "invalid_grant"

// refreshingTokenSource hands out a user's stored Yahoo token and refreshes it
// when the access token expires. It differs from the oauth2.TokenSource the
// config would give us in two ways that matter for a token shared by many
// short-lived clients across several processes:
//
//   - every refresh is written back to Redis, so it survives this client and
//     is picked up by the next one instead of each client refreshing anew;
//   - refreshes are serialized per user with a Redis lock, and the stored
//     record is re-read under the lock, so concurrent clients never present
//     the same refresh token to Yahoo at once.
type refreshingTokenSource struct {
	// ctx is captured because oauth2.TokenSource.Token() takes none; the
	// oauth2 package's own token sources make the same trade-off.
	ctx         context.Context
	redisClient *redis.Client
	conf        *oauth2.Config
	user        string

	// mu guards current, and is held across the whole refresh (Redis lock
	// plus the Yahoo round-trip) so one source never refreshes twice.
	mu      sync.Mutex
	current *oauth2.Token
}

// newRefreshingTokenSource loads the user's stored token. It fails with
// *cache.OAuth2TokenMissingError when the user has never logged in (or has
// signed out), which callers turn into a login prompt.
func newRefreshingTokenSource(ctx context.Context, redisClient *redis.Client, conf *oauth2.Config, user string) (*refreshingTokenSource, error) {
	token, err := cache.LoadTokenForUser(ctx, redisClient, user)
	if err != nil {
		return nil, err
	}
	return &refreshingTokenSource{ctx: ctx, redisClient: redisClient, conf: conf, user: user, current: token}, nil
}

// Token implements oauth2.TokenSource.
func (s *refreshingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.Valid() {
		return s.current, nil
	}
	token, err := s.refresh()
	if err != nil {
		return nil, err
	}
	s.current = token
	return token, nil
}

// refresh obtains a valid token under the user's refresh lock: whatever is
// stored once the lock is held wins if it is still valid (another process got
// there first); otherwise the stored refresh token mints a new access token,
// which is persisted before the lock is released.
func (s *refreshingTokenSource) refresh() (*oauth2.Token, error) {
	release, err := cache.LockTokenRefresh(s.ctx, s.redisClient, s.user)
	if err != nil {
		return nil, err
	}
	defer release()

	stored, err := cache.LoadTokenForUser(s.ctx, s.redisClient, s.user)
	if err != nil {
		return nil, err
	}
	if stored.Valid() {
		log.Debug().Str("user", s.user).Msg("Using token refreshed by another process")
		return stored, nil
	}

	fresh, err := s.conf.TokenSource(s.ctx, stored).Token()
	if err != nil {
		return nil, s.refreshFailed(err)
	}
	if err := cache.SaveTokenForUser(s.ctx, s.redisClient, s.user, fresh); err != nil {
		return nil, err
	}
	log.Info().Str("user", s.user).Time("expiry", fresh.Expiry).Msg("Refreshed authentication token")
	return fresh, nil
}

// refreshRejected reports whether the provider definitively refused the
// refresh token, as opposed to failing to answer. The RFC 6749 invalid_grant
// code is the explicit signal, but Yahoo's error bodies do not always follow
// the RFC, so a 400/401/403 without a parsable code counts too. Rate limiting
// (429) and 5xx are transient and keep the stored record.
func refreshRejected(retrieveErr *oauth2.RetrieveError) bool {
	if retrieveErr.ErrorCode == oauthErrorInvalidGrant {
		return true
	}
	if retrieveErr.Response == nil {
		return false
	}
	switch retrieveErr.Response.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return true
	}
	return false
}

// refreshFailed turns a rejected refresh into a login prompt. The record is
// deleted so status reporting stops calling the user logged in: now that
// records carry no TTL, nothing else would ever clear a dead credential.
// Transient failures (network, 429, 5xx) pass through with the record kept.
func (s *refreshingTokenSource) refreshFailed(err error) error {
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) || !refreshRejected(retrieveErr) {
		return fmt.Errorf("refresh token for %s: %w", s.user, err)
	}
	log.Warn().Str("user", s.user).Msg("Yahoo rejected the stored refresh token; login required")
	if delErr := cache.DeleteTokenForUser(s.ctx, s.redisClient, s.user); delErr != nil {
		log.Error().Err(delErr).Str("user", s.user).Msg("Failed to delete rejected token")
	}
	return &cache.OAuth2TokenMissingError{Reason: "yahoo rejected the stored refresh token"}
}
