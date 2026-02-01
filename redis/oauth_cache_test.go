package redis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestTokenAsString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token *oauth2.Token
	}{
		{
			name: "basic token",
			token: &oauth2.Token{
				AccessToken:  "access123",
				TokenType:    "Bearer",
				RefreshToken: "refresh456",
				Expiry:       time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "token without refresh",
			token: &oauth2.Token{
				AccessToken: "access789",
				TokenType:   "Bearer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := TokenAsString(tt.token)
			require.NoError(t, err)

			// Verify it's valid JSON
			var parsed oauth2.Token
			err = json.Unmarshal([]byte(result), &parsed)
			require.NoError(t, err)
			assert.Equal(t, tt.token.AccessToken, parsed.AccessToken)
		})
	}
}

func TestGetRedisKeyForToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		user     string
		expected string
	}{
		{"testuser", "yahoo:oauth2:testuser"},
		{"admin", "yahoo:oauth2:admin"},
	}

	for _, tt := range tests {
		t.Run(tt.user, func(t *testing.T) {
			key := getRedisKeyForToken(tt.user)
			assert.Contains(t, key, tt.user)
		})
	}
}

func TestSaveToken(t *testing.T) {
	t.Parallel()

	t.Run("saves token with TTL", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")

		token := &oauth2.Token{
			AccessToken:  "access123",
			TokenType:    "Bearer",
			RefreshToken: "refresh456",
			Expiry:       time.Now().Add(1 * time.Hour),
		}

		statusCmd := redis.NewStatusCmd(ctx)
		statusCmd.SetVal("OK")
		mockClient.On("Set", ctx, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("time.Duration")).Return(statusCmd)

		err := SaveToken(ctx, mockClient, token)
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("saves token with zero expiry", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")

		token := &oauth2.Token{
			AccessToken: "access123",
			TokenType:   "Bearer",
		}

		statusCmd := redis.NewStatusCmd(ctx)
		statusCmd.SetVal("OK")
		mockClient.On("Set", ctx, mock.AnythingOfType("string"), mock.AnythingOfType("string"), time.Duration(0)).Return(statusCmd)

		err := SaveToken(ctx, mockClient, token)
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("fails without user in context", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		token := &oauth2.Token{
			AccessToken: "access123",
		}

		err := SaveToken(ctx, mockClient, token)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "saving authentication token")
	})

	t.Run("handles expired token with negative TTL", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")

		token := &oauth2.Token{
			AccessToken: "access123",
			Expiry:      time.Now().Add(-1 * time.Hour), // Already expired
		}

		statusCmd := redis.NewStatusCmd(ctx)
		statusCmd.SetVal("OK")
		mockClient.On("Set", ctx, mock.AnythingOfType("string"), mock.AnythingOfType("string"), time.Duration(0)).Return(statusCmd)

		err := SaveToken(ctx, mockClient, token)
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})
}

func TestLoadToken(t *testing.T) {
	t.Parallel()

	t.Run("fails without user in context", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		_, err := LoadToken(ctx, mockClient)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "loading authentication token")
	})

	t.Run("loads token with user in context", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")

		tokenJSON := `{"access_token":"access123","token_type":"Bearer","refresh_token":"refresh456"}`
		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetVal(tokenJSON)
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		token, err := LoadToken(ctx, mockClient)
		require.NoError(t, err)
		assert.Equal(t, "access123", token.AccessToken)
		mockClient.AssertExpectations(t)
	})
}

func TestLoadTokenForUser(t *testing.T) {
	t.Parallel()

	t.Run("loads existing token", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		tokenJSON := `{"access_token":"access123","token_type":"Bearer","refresh_token":"refresh456"}`
		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetVal(tokenJSON)
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		token, err := LoadTokenForUser(ctx, mockClient, "testuser")
		require.NoError(t, err)
		assert.Equal(t, "access123", token.AccessToken)
		assert.Equal(t, "Bearer", token.TokenType)
		assert.Equal(t, "refresh456", token.RefreshToken)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns missing error when token not found", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetErr(redis.Nil)
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		_, err := LoadTokenForUser(ctx, mockClient, "testuser")
		require.Error(t, err)
		assert.True(t, errors.Is(err, NewOAuth2TokenMissingError()))
		mockClient.AssertExpectations(t)
	})

	t.Run("returns cache read error on other errors", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetErr(errors.New("connection refused"))
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		_, err := LoadTokenForUser(ctx, mockClient, "testuser")
		require.Error(t, err)
		var cacheErr *CacheReadError
		assert.True(t, errors.As(err, &cacheErr))
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetVal("invalid json")
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		_, err := LoadTokenForUser(ctx, mockClient, "testuser")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error unmarshalling")
		mockClient.AssertExpectations(t)
	})
}

func TestDeleteTokenForUser(t *testing.T) {
	t.Parallel()

	t.Run("deletes token successfully", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		intCmd := redis.NewIntCmd(ctx)
		intCmd.SetVal(1)
		mockClient.On("Del", ctx, mock.AnythingOfType("string")).Return(intCmd)

		err := DeleteTokenForUser(ctx, mockClient, "testuser")
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("handles delete error", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		intCmd := redis.NewIntCmd(ctx)
		intCmd.SetErr(errors.New("connection refused"))
		mockClient.On("Del", ctx, mock.AnythingOfType("string")).Return(intCmd)

		err := DeleteTokenForUser(ctx, mockClient, "testuser")
		require.Error(t, err)
		mockClient.AssertExpectations(t)
	})
}

func TestMarkAuthCodeAsUsed(t *testing.T) {
	t.Parallel()

	t.Run("marks new code as used", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		boolCmd := redis.NewBoolCmd(ctx)
		boolCmd.SetVal(true)
		mockClient.On("SetNX", ctx, mock.AnythingOfType("string"), "used", 10*time.Minute).Return(boolCmd)

		err := MarkAuthCodeAsUsed(ctx, mockClient, "authcode123")
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error for already used code", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		boolCmd := redis.NewBoolCmd(ctx)
		boolCmd.SetVal(false)
		mockClient.On("SetNX", ctx, mock.AnythingOfType("string"), "used", 10*time.Minute).Return(boolCmd)

		err := MarkAuthCodeAsUsed(ctx, mockClient, "authcode123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already used")
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error on redis failure", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		boolCmd := redis.NewBoolCmd(ctx)
		boolCmd.SetErr(errors.New("connection refused"))
		mockClient.On("SetNX", ctx, mock.AnythingOfType("string"), "used", 10*time.Minute).Return(boolCmd)

		err := MarkAuthCodeAsUsed(ctx, mockClient, "authcode123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to mark auth code")
		mockClient.AssertExpectations(t)
	})
}

func TestHasValidToken(t *testing.T) {
	t.Parallel()

	t.Run("returns true for valid non-expired token", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		token := &oauth2.Token{
			AccessToken: "access123",
			Expiry:      time.Now().Add(1 * time.Hour),
		}
		tokenJSON, _ := json.Marshal(token)

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetVal(string(tokenJSON))
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		valid, err := HasValidToken(ctx, mockClient, "testuser")
		require.NoError(t, err)
		assert.True(t, valid)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns true for token with zero expiry", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		token := &oauth2.Token{
			AccessToken: "access123",
		}
		tokenJSON, _ := json.Marshal(token)

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetVal(string(tokenJSON))
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		valid, err := HasValidToken(ctx, mockClient, "testuser")
		require.NoError(t, err)
		assert.True(t, valid)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns false for expired token", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		token := &oauth2.Token{
			AccessToken: "access123",
			Expiry:      time.Now().Add(-1 * time.Hour),
		}
		tokenJSON, _ := json.Marshal(token)

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetVal(string(tokenJSON))
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		valid, err := HasValidToken(ctx, mockClient, "testuser")
		require.NoError(t, err)
		assert.False(t, valid)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns false when token not found", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetErr(redis.Nil)
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		valid, err := HasValidToken(ctx, mockClient, "testuser")
		require.NoError(t, err)
		assert.False(t, valid)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error on other failures", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetErr(errors.New("connection refused"))
		mockClient.On("Get", ctx, mock.AnythingOfType("string")).Return(stringCmd)

		_, err := HasValidToken(ctx, mockClient, "testuser")
		require.Error(t, err)
		mockClient.AssertExpectations(t)
	})
}
