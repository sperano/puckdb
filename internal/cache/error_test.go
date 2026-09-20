package cache

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOAuth2TokenMissingError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         *OAuth2TokenMissingError
		wantMessage string
	}{
		{
			name:        "no public URL uses relative path",
			err:         &OAuth2TokenMissingError{},
			wantMessage: "no oauth2 token found in redis. Please navigate to /yahoo/login to get a new token",
		},
		{
			name:        "with public URL prefixes login path",
			err:         &OAuth2TokenMissingError{PublicURL: "https://example.com"},
			wantMessage: "no oauth2 token found in redis. Please navigate to https://example.com/yahoo/login to get a new token",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantMessage, tc.err.Error())
		})
	}
}

func TestCacheReadError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		key         string
		wrapped     error
		wantContain []string
	}{
		{
			name:        "includes key and wrapped error message",
			key:         "schedule:2024-01-01",
			wrapped:     errors.New("connection refused"),
			wantContain: []string{"schedule:2024-01-01", "connection refused"},
		},
		{
			name:        "includes error reading prefix",
			key:         "boxscore:12345",
			wrapped:     errors.New("timeout"),
			wantContain: []string{"error reading", "boxscore:12345", "timeout"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := &CacheReadError{Key: tc.key, Err: tc.wrapped}
			msg := err.Error()
			for _, want := range tc.wantContain {
				assert.Contains(t, msg, want)
			}
		})
	}
}

func TestCacheReadError_Unwrap(t *testing.T) {
	t.Parallel()

	t.Run("returns wrapped error and is matchable via errors.Is", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("redis down")
		err := &CacheReadError{Key: "schedule:2024-01-01", Err: sentinel}
		assert.Same(t, sentinel, err.Unwrap())
		assert.True(t, errors.Is(err, sentinel),
			"errors.Is should walk Unwrap and find the sentinel")
	})

	t.Run("nil wrapped error stays nil", func(t *testing.T) {
		t.Parallel()
		err := &CacheReadError{Key: "k"}
		assert.Nil(t, err.Unwrap())
	})
}

func TestTokenDecodeError_Error(t *testing.T) {
	t.Parallel()

	err := &TokenDecodeError{Key: "user_yahoo_oauth2_token", Reason: "malformed JSON"}
	assert.Equal(t, "error decoding token user_yahoo_oauth2_token from cache: malformed JSON", err.Error())
}

func TestDescribeJSONError(t *testing.T) {
	t.Parallel()

	// decodeErr produces the genuine encoding/json error for a payload.
	decodeErr := func(payload string) error {
		var target struct {
			AccessToken string `json:"access_token"`
		}
		return json.Unmarshal([]byte(payload), &target)
	}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"syntax error reports offset only", decodeErr(`{"access_token":?`), "JSON syntax error at offset 17"},
		{"type error reports field and offset", decodeErr(`{"access_token":98765}`), `field "access_token" has the wrong JSON type at offset 21`},
		{"unknown error reports its type only", errors.New("secret-bearing message"), "malformed JSON (*errors.errorString)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, describeJSONError(tc.err))
		})
	}
}
