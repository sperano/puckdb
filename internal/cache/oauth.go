package cache

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"golang.org/x/oauth2"
)

// tokenClient is the minimal Redis surface used by oauth2 token storage and
// auth-code marking. Declared on the consumer side so callers can pass any
// implementation; production uses *redis.Client.
type tokenClient interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd
}

// TokenAsString serializes an OAuth2 token to JSON string. The result carries
// the access and refresh tokens: it is for storage only and must never be
// logged or embedded in an error.
func TokenAsString(token *oauth2.Token) (string, error) {
	b, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func getRedisKeyForToken(user string) string {
	return fmt.Sprintf(config.RedisKeyYahooTokenFmt, user)
}

// ErrTokenExpired is returned when saving a token whose access token has
// already expired and that carries no refresh token: it can never serve a
// request again, so storing it would only leave a dead credential in Redis.
var ErrTokenExpired = errors.New("token has expired and cannot be refreshed")

// persistIndefinitely is the go-redis expiration meaning "no TTL".
const persistIndefinitely time.Duration = 0

// tokenRecordTTL decides how long a stored token record lives. A record that
// carries a refresh token outlives its access token: the refresh token is what
// lets a request after an idle period mint a new access token without an
// interactive login, so it stays until sign-out deletes it or Yahoo rejects it.
// A record without a refresh token is useless past its access token's expiry
// and expires with it.
func tokenRecordTTL(token *oauth2.Token) (time.Duration, error) {
	if token.RefreshToken != "" || token.Expiry.IsZero() {
		return persistIndefinitely, nil
	}
	ttl := time.Until(token.Expiry)
	if ttl <= 0 {
		return 0, ErrTokenExpired
	}
	return ttl, nil
}

// SaveToken saves an OAuth2 token to the cache for the current user.
func SaveToken(ctx context.Context, redisClient tokenClient, token *oauth2.Token) error {
	user, err := config.UserFromContext(ctx)
	if err != nil {
		return fmt.Errorf("saving authentication token: %w", err)
	}
	return SaveTokenForUser(ctx, redisClient, user, token)
}

// SaveTokenForUser saves an OAuth2 token to the cache for a specific user. The
// record's lifetime follows tokenRecordTTL, not the access token's expiry.
func SaveTokenForUser(ctx context.Context, redisClient tokenClient, user string, token *oauth2.Token) error {
	s, err := TokenAsString(token)
	if err != nil {
		return err
	}
	ttl, err := tokenRecordTTL(token)
	if err != nil {
		return fmt.Errorf("saving authentication token for %s: %w", user, err)
	}
	key := getRedisKeyForToken(user)

	log.Info().Str("user", user).Time("expiry", token.Expiry).Dur("ttl", ttl).
		Bool("refreshable", token.RefreshToken != "").Msg("Saving authentication token")
	return redisClient.Set(ctx, key, s, ttl).Err()
}

// LoadToken loads an OAuth2 token from the cache for the current user.
func LoadToken(ctx context.Context, redisClient tokenClient) (*oauth2.Token, error) {
	user, err := config.UserFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading authentication token: %w", err)
	}
	return LoadTokenForUser(ctx, redisClient, user)
}

// LoadTokenForUser loads an OAuth2 token from the cache for a specific user.
func LoadTokenForUser(ctx context.Context, redisClient tokenClient, user string) (*oauth2.Token, error) {
	key := getRedisKeyForToken(user)
	log.Debug().Str("key", key).Msg("Loading authentication token")
	status := redisClient.Get(ctx, key)
	if err := status.Err(); err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, &OAuth2TokenMissingError{}
		}
		return nil, &CacheReadError{Key: key, Err: err}
	}
	tokenAsString, err := status.Result()
	if err != nil {
		return nil, fmt.Errorf("error converting %s from %w", key, err)
	}
	var token oauth2.Token
	if err := json.Unmarshal([]byte(tokenAsString), &token); err != nil {
		// The payload holds live credentials, so only the key identifies it.
		return nil, newTokenDecodeError(key, err)
	}
	return &token, nil
}

// DeleteTokenForUser deletes an OAuth2 token from the cache for a specific user.
func DeleteTokenForUser(ctx context.Context, redisClient tokenClient, user string) error {
	key := getRedisKeyForToken(user)
	log.Debug().Str("key", key).Msg("Deleting authentication token")
	return redisClient.Del(ctx, key).Err()
}

// getRedisKeyForAuthCode derives the key from a digest of the code, never the
// code itself, so the credential is not spelled out wherever keys surface
// (Redis tooling, tracing, cache errors). Yahoo's codes are short, so an
// unsalted digest can be brute-forced: treat the key as sensitive and never
// log it.
func getRedisKeyForAuthCode(code string) string {
	digest := sha256.Sum256([]byte(code))
	return fmt.Sprintf(config.RedisKeyYahooAuthCodeFmt, hex.EncodeToString(digest[:]))
}

// MarkAuthCodeAsUsed marks an authorization code as used to prevent replay attacks.
// Authorization codes are single-use only per OAuth2 spec.
// Returns error if code was already used.
func MarkAuthCodeAsUsed(ctx context.Context, redisClient tokenClient, code string) error {
	key := getRedisKeyForAuthCode(code)
	const codeTTL = 10 * time.Minute

	// SetNX returns true only if key didn't exist (atomic check-and-set)
	success, err := redisClient.SetNX(ctx, key, "used", codeTTL).Result()
	if err != nil {
		return fmt.Errorf("mark auth code as used: %w", err)
	}
	if !success {
		return fmt.Errorf("authorization code already used")
	}
	log.Debug().Msg("Marked authorization code as used")
	return nil
}

// LoginStateTTL bounds how long a browser has to finish the Yahoo consent
// screen once redirected there. The state cookie and the Redis entry share it.
const LoginStateTTL = 10 * time.Minute

// loginStateBytes is the entropy of a login state; 32 bytes makes guessing or
// colliding a pending state infeasible.
const loginStateBytes = 32

// ErrLoginStateInvalid is returned by ConsumeLoginState when the state was
// never issued, has expired, or was already consumed by an earlier callback.
var ErrLoginStateInvalid = errors.New("login state is unknown, expired, or already used")

func getRedisKeyForLoginState(state string) string {
	return fmt.Sprintf(config.RedisKeyYahooLoginStateFmt, state)
}

// NewLoginState issues a random OAuth2 state for one login attempt and records
// it as pending for LoginStateTTL. The caller sends it in the authorization URL
// and binds it to the browser (cookie); ConsumeLoginState later proves the
// callback belongs to a login this server started.
func NewLoginState(ctx context.Context, redisClient tokenClient) (string, error) {
	buf := make([]byte, loginStateBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate login state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(buf)

	created, err := redisClient.SetNX(ctx, getRedisKeyForLoginState(state), "pending", LoginStateTTL).Result()
	if err != nil {
		return "", fmt.Errorf("register login state: %w", err)
	}
	if !created {
		return "", errors.New("register login state: state already pending")
	}
	return state, nil
}

// ConsumeLoginState atomically retires a pending state so it can complete at
// most one login. DEL reports how many keys it removed, so 0 means the state
// was never issued, has expired, or was already consumed.
func ConsumeLoginState(ctx context.Context, redisClient tokenClient, state string) error {
	removed, err := redisClient.Del(ctx, getRedisKeyForLoginState(state)).Result()
	if err != nil {
		return fmt.Errorf("consume login state: %w", err)
	}
	if removed == 0 {
		return ErrLoginStateInvalid
	}
	return nil
}

// TokenUsable reports whether a stored token can still serve a Yahoo request:
// either its access token is valid, or it carries a refresh token to mint a
// new one. Use HasValidToken when only the current access token matters.
func TokenUsable(token *oauth2.Token) bool {
	return token.Valid() || token.RefreshToken != ""
}

// HasValidToken reports whether the user's stored access token is still
// within its validity window, i.e. a request can be made without refreshing.
// A missing token is (false, nil); a Redis failure is an error.
func HasValidToken(ctx context.Context, redisClient tokenClient, user string) (bool, error) {
	return checkStoredToken(ctx, redisClient, user, (*oauth2.Token).Valid)
}

// HasUsableToken reports whether the user holds credentials that can serve a
// Yahoo request, refreshing if needed (see TokenUsable). This is what "logged
// in" means to status reporting: an expired access token next to a refresh
// token is not a reason to log in again.
func HasUsableToken(ctx context.Context, redisClient tokenClient, user string) (bool, error) {
	return checkStoredToken(ctx, redisClient, user, TokenUsable)
}

func checkStoredToken(ctx context.Context, redisClient tokenClient, user string, check func(*oauth2.Token) bool) (bool, error) {
	token, err := LoadTokenForUser(ctx, redisClient, user)
	if err != nil {
		var tokenErr *OAuth2TokenMissingError
		if errors.As(err, &tokenErr) {
			return false, nil
		}
		return false, err
	}
	return check(token), nil
}
