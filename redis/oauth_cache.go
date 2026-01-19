package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	redis_ "github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/auth"
	"golang.org/x/oauth2"
)

func TokenAsString(token *oauth2.Token) (string, error) {
	b, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func getRedisKeyForToken(user string) string {
	return fmt.Sprintf("%s_yahoo_oauth2_token", user)
}

func SaveToken(ctx context.Context, redisClient Client, token *oauth2.Token) error {
	s, err := TokenAsString(token)
	if err != nil {
		return err
	}
	user, err := auth.UserFromContext(ctx)
	if err != nil {
		return fmt.Errorf("saving authentication token: %w", err)
	}
	key := getRedisKeyForToken(user)

	var ttl time.Duration
	if !token.Expiry.IsZero() {
		ttl = time.Until(token.Expiry)
		if ttl < 0 {
			ttl = 0
		}
	}

	log.Info().Str("key", key).Str("token", s).Dur("ttl", ttl).Msg("Saving")
	return redisClient.Set(ctx, key, s, ttl).Err()
}

func LoadToken(ctx context.Context, redisClient Client) (*oauth2.Token, error) {
	user, err := auth.UserFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading authentication token: %w", err)
	}
	return LoadTokenForUser(ctx, redisClient, user)
}

func LoadTokenForUser(ctx context.Context, redisClient Client, user string) (*oauth2.Token, error) {
	key := getRedisKeyForToken(user)
	log.Debug().Str("key", key).Msg("Loading authentication token")
	status := redisClient.Get(ctx, key)
	if err := status.Err(); err != nil {
		if errors.Is(err, redis_.Nil) {
			return nil, NewOAuth2TokenMissingError()
		}
		return nil, NewCacheReadError(key, err)
	}
	tokenAsString, err := status.Result()
	if err != nil {
		return nil, fmt.Errorf("error converting %s from %w", key, err)
	}
	var token oauth2.Token
	if err := json.Unmarshal([]byte(tokenAsString), &token); err != nil {
		return nil, fmt.Errorf("error unmarshalling %s from %v", tokenAsString, err)
	}
	return &token, nil
}

func DeleteTokenForUser(ctx context.Context, redisClient Client, user string) error {
	key := getRedisKeyForToken(user)
	log.Debug().Str("key", key).Msg("Deleting authentication token")
	return redisClient.Del(ctx, key).Err()
}

func getRedisKeyForAuthCode(code string) string {
	return fmt.Sprintf("yahoo_oauth2_code_%s", code)
}

// MarkAuthCodeAsUsed marks an authorization code as used to prevent replay attacks.
// Authorization codes are single-use only per OAuth2 spec.
// Returns error if code was already used.
func MarkAuthCodeAsUsed(ctx context.Context, redisClient Client, code string) error {
	key := getRedisKeyForAuthCode(code)
	const code_ttl = 10 * time.Minute

	// SetNX returns true only if key didn't exist (atomic check-and-set)
	success, err := redisClient.SetNX(ctx, key, "used", code_ttl).Result()
	if err != nil {
		return fmt.Errorf("failed to mark auth code as used: %w", err)
	}
	if !success {
		return fmt.Errorf("authorization code already used")
	}
	log.Debug().Str("key", key).Msg("Marked authorization code as used")
	return nil
}

// HasValidToken checks if a valid (non-expired) token exists for the user
func HasValidToken(ctx context.Context, redisClient Client, user string) (bool, error) {
	token, err := LoadTokenForUser(ctx, redisClient, user)
	if err != nil {
		if errors.Is(err, NewOAuth2TokenMissingError()) {
			return false, nil
		}
		return false, err
	}
	// Check if token is expired
	if token.Expiry.IsZero() || time.Now().Before(token.Expiry) {
		return true, nil
	}
	return false, nil
}

