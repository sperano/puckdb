package http

import (
	"context"
	"fmt"
	"github.com/sperano/puckdb/auth"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/redis"
	"golang.org/x/oauth2"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/spf13/viper"
)

func handleError(w http.ResponseWriter, code int, err error) {
	log.Error().Err(err)
	json := fmt.Sprintf(`{"error":"%s"}`, err)
	http.Error(w, json, code)
}

func setNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func YahooLoginHandler(w http.ResponseWriter, r *http.Request) {
	conf, err := config.OauthConfig()
	if err != nil {
		handleError(w, http.StatusInternalServerError, err)
		return
	}
	YahooLoginHandlerWithConfig(conf)(w, r)
}

func YahooLoginHandlerWithConfig(conf *oauth2.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setNoCacheHeaders(w)
		url := conf.AuthCodeURL("state", oauth2.AccessTypeOnline)
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
	w.Write([]byte(html))
}

func YahooAuthenticatedHandler(redisClient redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setNoCacheHeaders(w)

		// Check for OAuth error response first (before loading config)
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			errDesc := r.URL.Query().Get("error_description")
			handleError(w, http.StatusForbidden, fmt.Errorf("OAuth error: %s - %s", errParam, errDesc))
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			handleError(w, http.StatusBadRequest, fmt.Errorf("missing authorization code"))
			return
		}

		conf, err := config.OauthConfig()
		if err != nil {
			handleError(w, http.StatusInternalServerError, err)
			return
		}
		successURL := fmt.Sprintf("%s%s", viper.GetString(config.FlagPublicURL), config.YahooLandedPath)
		YahooAuthenticatedHandlerWithConfig(redisClient, conf, successURL, code)(w, r)
	}
}

func YahooAuthenticatedHandlerWithConfig(redisClient redis.Client, conf *oauth2.Config, successURL string, code string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setNoCacheHeaders(w)

		if viper.GetBool(config.FlagYahooLogToken) {
			log.Debug().Str("code", code).Msg("Authentication code received from Yahoo")
		}
		ctxV := context.WithValue(context.Background(), auth.CtxUser, config.DefaultUser)
		err := exchangeCodeWithConfig(ctxV, redisClient, conf, config.DefaultUser, code)
		if err == nil {
			http.Redirect(w, r, successURL, http.StatusFound)
		} else {
			handleError(w, http.StatusForbidden, fmt.Errorf("should probably authenticate again: %w", err))
		}
	}
}

func exchangeCode(ctx context.Context, redisClient redis.Client, user string, code string) error {
	conf, err := config.OauthConfig()
	if err != nil {
		return fmt.Errorf("failed to get OAuth config: %w", err)
	}
	return exchangeCodeWithConfig(ctx, redisClient, conf, user, code)
}

func exchangeCodeWithConfig(ctx context.Context, redisClient redis.Client, conf *oauth2.Config, user string, code string) error {
	// Step 1: Check if we already have a valid token
	// This prevents unnecessary code exchanges and protects against callback replays
	hasToken, err := redis.HasValidToken(ctx, redisClient, user)
	if err != nil {
		log.Error().Err(err).Msg("Failed to check for existing token")
		// Continue anyway - this is a safeguard, not a blocker
	}
	if hasToken {
		log.Info().Str("user", user).Msg("User already has valid token, skipping code exchange")
		return nil
	}

	// Step 2: Mark authorization code as used BEFORE attempting exchange
	// This ensures we never retry with the same code even if exchange succeeds but save fails
	// Uses Redis SetNX for atomic check-and-set (prevents race conditions)
	if err := redis.MarkAuthCodeAsUsed(ctx, redisClient, code); err != nil {
		log.Error().Err(err).Msg("Authorization code already used or failed to mark as used")
		return fmt.Errorf("cannot exchange authorization code: %w", err)
	}

	// Step 3: Exchange code for token
	token, err := conf.Exchange(ctx, code)
	if err != nil {
		// Exchange failed - code is marked as used but we have no token
		// This is the correct behavior per OAuth2 spec (codes are single-use)
		log.Error().Err(err).Msg("OAuth2 token exchange failed")
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Step 4: Persist token to Redis
	// If this fails, we have a token but it's not saved - this is critical
	if err := redis.SaveToken(ctx, redisClient, token); err != nil {
		log.Error().Err(err).Msg("CRITICAL: Token exchange succeeded but Redis save failed")
		// Even though we have the token, we return an error because it's not persisted
		// The code is consumed, user will need to re-authenticate
		return fmt.Errorf("token save failed (code consumed, please re-authenticate): %w", err)
	}

	log.Info().Str("user", user).Msg("Successfully exchanged authorization code and saved token")
	return nil
}
