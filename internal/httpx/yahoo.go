package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"golang.org/x/oauth2"
)

func handleError(w http.ResponseWriter, code int, err error) {
	log.Error().Err(err).Msg("HTTP error")
	resp := struct {
		Error string `json:"error"`
	}{Error: err.Error()}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

func setNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

// YahooLoginHandler starts a Yahoo login with the OAuth2 application in auth.
func YahooLoginHandler(redisClient *redis.Client, auth YahooAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conf, err := auth.OAuth2Config()
		if err != nil {
			handleError(w, http.StatusInternalServerError, err)
			return
		}
		YahooLoginHandlerWithConfig(redisClient, conf)(w, r)
	}
}

// YahooLoginHandlerWithConfig starts one login attempt: it issues a random
// state, binds it to this browser with a cookie, and sends the browser to
// Yahoo with the same state so the callback can be verified.
func YahooLoginHandlerWithConfig(redisClient *redis.Client, conf *oauth2.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setNoCacheHeaders(w)
		state, err := cache.NewLoginState(r.Context(), redisClient)
		if err != nil {
			handleError(w, http.StatusInternalServerError, err)
			return
		}
		http.SetCookie(w, newLoginStateCookie(r, state))
		url := conf.AuthCodeURL(state, oauth2.AccessTypeOnline)
		http.Redirect(w, r, url, http.StatusFound)
	}
}

func YahooLandedHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := `<!DOCTYPE html>
<html>
<head><title>Login Successful</title></head>
<body>
<h1>Login Successful</h1>
<p>You have successfully logged in to Yahoo.</p>
<p><a href="/yahoo/login">Refresh token</a></p>
</body>
</html>`
	_, _ = w.Write([]byte(html))
}

// callbackConfig resolves the OAuth config and the post-login redirect. It is
// called only after the callback has been validated, so a rejected request
// never needs (or reveals problems with) the OAuth configuration.
type callbackConfig func() (conf *oauth2.Config, successURL string, err error)

// YahooAuthenticatedHandler completes a Yahoo login started by
// YahooLoginHandler with the same auth, then redirects to the landed page.
func YahooAuthenticatedHandler(redisClient *redis.Client, auth YahooAuth) http.HandlerFunc {
	return yahooAuthenticatedHandler(redisClient, func() (*oauth2.Config, string, error) {
		conf, err := auth.OAuth2Config()
		if err != nil {
			return nil, "", err
		}
		return conf, auth.PublicURL + config.YahooLandedPath, nil
	})
}

// yahooAuthenticatedHandler handles Yahoo's redirect back to us. The order
// matters: the callback is bound to the login this browser started before the
// authorization code is looked at, so an unsolicited or replayed callback can
// never reach the token exchange or overwrite the stored token.
func yahooAuthenticatedHandler(redisClient *redis.Client, resolve callbackConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setNoCacheHeaders(w)
		// The cookie is single-use: drop it whatever the outcome.
		http.SetCookie(w, clearLoginStateCookie(r))

		if errParam := r.URL.Query().Get("error"); errParam != "" {
			errDesc := r.URL.Query().Get("error_description")
			handleError(w, http.StatusForbidden, fmt.Errorf("OAuth error: %s - %s", errParam, errDesc))
			return
		}

		if err := validateLoginState(r.Context(), redisClient, r); err != nil {
			handleError(w, http.StatusForbidden, err)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			handleError(w, http.StatusBadRequest, fmt.Errorf("missing authorization code"))
			return
		}

		conf, successURL, err := resolve()
		if err != nil {
			handleError(w, http.StatusInternalServerError, err)
			return
		}

		// The code is a credential: it is never logged.
		log.Debug().Msg("Authorization code received from Yahoo")
		ctxV := context.WithValue(r.Context(), config.CtxUser, config.DefaultUser)
		if err := exchangeCodeWithConfig(ctxV, redisClient, conf, config.DefaultUser, code); err != nil {
			handleError(w, http.StatusForbidden, fmt.Errorf("should probably authenticate again: %w", err))
			return
		}
		http.Redirect(w, r, successURL, http.StatusFound)
	}
}

func exchangeCodeWithConfig(ctx context.Context, redisClient *redis.Client, conf *oauth2.Config, user string, code string) error {
	// Serialize login with background refreshes so an in-flight refresh cannot
	// overwrite credentials obtained by this explicit reauthorization.
	release, err := cache.LockTokenRefresh(ctx, redisClient, user)
	if err != nil {
		return fmt.Errorf("lock token replacement: %w", err)
	}
	defer release()

	// Mark authorization code as used BEFORE attempting exchange
	// This ensures we never retry with the same code even if exchange succeeds but save fails
	// Uses Redis SetNX for atomic check-and-set (prevents race conditions)
	if err := cache.MarkAuthCodeAsUsed(ctx, redisClient, code); err != nil {
		log.Error().Err(err).Msg("Authorization code already used or failed to mark as used")
		return fmt.Errorf("cannot exchange authorization code: %w", err)
	}

	// Exchange code for token
	token, err := conf.Exchange(ctx, code)
	if err != nil {
		// Exchange failed - code is marked as used but we have no token
		// This is the correct behavior per OAuth2 spec (codes are single-use)
		log.Error().Err(err).Msg("OAuth2 token exchange failed")
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Persist token to Redis
	// If this fails, we have a token but it's not saved - this is critical
	if err := cache.SaveTokenForUser(ctx, redisClient, user, token); err != nil {
		log.Error().Err(err).Msg("CRITICAL: Token exchange succeeded but Redis save failed")
		// Even though we have the token, we return an error because it's not persisted
		// The code is consumed, user will need to re-authenticate
		return fmt.Errorf("token save failed (code consumed, please re-authenticate): %w", err)
	}

	log.Info().Str("user", user).Msg("Successfully exchanged authorization code and saved token")
	return nil
}
