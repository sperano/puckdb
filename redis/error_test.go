package redis

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOAuth2TokenMissingError(t *testing.T) {
	t.Parallel()

	err := NewOAuth2TokenMissingError()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no oauth2 token found in redis")
	assert.Contains(t, err.Error(), "/yahoo/login")
}

func TestCacheReadError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		key         string
		originalErr error
	}{
		{"simple key", "player:123", errors.New("connection refused")},
		{"complex key", "oauth:token:user@example.com", errors.New("timeout")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewCacheReadError(tt.key, tt.originalErr)
			assert.NotNil(t, err)
			assert.Equal(t, tt.key, err.Key)
			assert.Equal(t, tt.originalErr, err.Err)
			assert.Contains(t, err.Error(), tt.key)
			assert.Contains(t, err.Error(), tt.originalErr.Error())
		})
	}
}
