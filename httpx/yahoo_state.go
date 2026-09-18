package httpx

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
)

// yahooCookiePath limits the login-state cookie to the OAuth routes; nothing
// else on the server needs to see it. Derived from the callback path so the
// browser keeps sending the cookie if that route is ever moved.
var yahooCookiePath = path.Dir(config.YahooAuthCallbackPath)

// newLoginStateCookie binds a freshly issued login state to the browser that
// requested the login. HttpOnly keeps scripts away from it; SameSite=Lax is
// required (not Strict) because Yahoo's callback is a top-level cross-site GET
// and Strict would withhold the cookie on that navigation.
func newLoginStateCookie(r *http.Request, state string) *http.Cookie {
	return &http.Cookie{
		Name:     config.YahooLoginStateCookie,
		Value:    state,
		Path:     yahooCookiePath,
		MaxAge:   int(cache.LoginStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	}
}

// clearLoginStateCookie tells the browser to drop the login-state cookie once
// the callback has been processed, whether or not it succeeded.
func clearLoginStateCookie(r *http.Request) *http.Cookie {
	c := newLoginStateCookie(r, "")
	c.MaxAge = -1
	return c
}

// requestIsHTTPS reports whether the browser reached us over TLS, either
// directly or through a proxy that terminated it (Envoy Gateway sets
// X-Forwarded-Proto). Drives the cookie's Secure flag.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// validateLoginState proves that a callback belongs to a login this browser
// started: the state parameter must be present, must equal the value in the
// login-state cookie, and must still be pending server-side. It consumes the
// state, so a second callback with the same state is rejected as a replay.
// Every failure returns before any token exchange or token write.
func validateLoginState(ctx context.Context, redisClient *redis.Client, r *http.Request) error {
	state := r.URL.Query().Get("state")
	if state == "" {
		return errors.New("missing state parameter")
	}
	cookie, err := r.Cookie(config.YahooLoginStateCookie)
	if err != nil {
		return errors.New("missing login state cookie; this browser did not start the login")
	}
	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		return errors.New("state does not match the login started by this browser")
	}
	if err := cache.ConsumeLoginState(ctx, redisClient, state); err != nil {
		return fmt.Errorf("login state rejected: %w", err)
	}
	return nil
}
