package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/oauth2"
)

func TestCmdInfoImpl_WithoutToken(t *testing.T) {
	mockRedis := &cache.MockClient{}
	ctx := context.Background()

	// Mock Get to return redis.Nil error (no token found)
	getCmd := goredis.NewStringCmd(ctx)
	getCmd.SetErr(goredis.Nil)
	mockRedis.On("Get", mock.Anything, mock.Anything).Return(getCmd)

	b := bytes.NewBufferString("")
	err := cmdInfoImpl(b, mockRedis)

	assert.NoError(t, err)
	output := b.String()

	// Strip ANSI color codes
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	output = ansiRegex.ReplaceAllString(output, "")

	assert.Contains(t, output, "Yahoo! Token:                  not found")
	assert.NotContains(t, output, "Access Token:")
}

func TestCmdInfoImpl_WithToken(t *testing.T) {
	mockRedis := &cache.MockClient{}
	ctx := context.Background()

	// Create a mock token
	expiry := time.Now().Add(1 * time.Hour)
	token := oauth2.Token{
		AccessToken:  "test-access-token",
		TokenType:    "Bearer",
		RefreshToken: "test-refresh-token",
		Expiry:       expiry,
	}
	tokenJSON, _ := json.Marshal(token)

	// Mock Get to return the token
	getCmd := goredis.NewStringCmd(ctx)
	getCmd.SetVal(string(tokenJSON))
	mockRedis.On("Get", mock.Anything, mock.Anything).Return(getCmd)

	b := bytes.NewBufferString("")
	err := cmdInfoImpl(b, mockRedis)

	assert.NoError(t, err)
	output := b.String()

	// Strip ANSI color codes
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	output = ansiRegex.ReplaceAllString(output, "")

	assert.Contains(t, output, "Yahoo! Token:                  found")
	assert.Contains(t, output, "Access Token:  test-access-token")
	assert.Contains(t, output, "Token Type:    Bearer")
	assert.Contains(t, output, "Refresh Token: test-refresh-token")
	assert.Contains(t, output, "Expires in:")
}

func TestCmdInfoImpl_WithExpiredToken(t *testing.T) {
	mockRedis := &cache.MockClient{}
	ctx := context.Background()

	// Create a mock expired token
	expiry := time.Now().Add(-1 * time.Hour)
	token := oauth2.Token{
		AccessToken:  "expired-access-token",
		TokenType:    "Bearer",
		RefreshToken: "expired-refresh-token",
		Expiry:       expiry,
	}
	tokenJSON, _ := json.Marshal(token)

	// Mock Get to return the token
	getCmd := goredis.NewStringCmd(ctx)
	getCmd.SetVal(string(tokenJSON))
	mockRedis.On("Get", mock.Anything, mock.Anything).Return(getCmd)

	b := bytes.NewBufferString("")
	err := cmdInfoImpl(b, mockRedis)

	assert.NoError(t, err)
	output := b.String()

	// Strip ANSI color codes
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	output = ansiRegex.ReplaceAllString(output, "")

	assert.Contains(t, output, "Yahoo! Token:                  found")
	assert.Contains(t, output, "Expired:")
	assert.Contains(t, output, "ago")
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "seconds",
			duration: 45 * time.Second,
			expected: "45 seconds",
		},
		{
			name:     "minutes",
			duration: 30 * time.Minute,
			expected: "30 minutes",
		},
		{
			name:     "hours and minutes",
			duration: 2*time.Hour + 30*time.Minute,
			expected: "2 hours, 30 minutes",
		},
		{
			name:     "days and hours",
			duration: 3*24*time.Hour + 5*time.Hour,
			expected: "3 days, 5 hours",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}
