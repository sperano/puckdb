package httpx

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2/yahoo"
)

func TestYahooAuth_OAuth2Config(t *testing.T) {
	t.Parallel()
	cfg, err := YahooAuth{
		ClientID: "test-client-id", ClientSecret: "test-client-secret", PublicURL: "https://example.com",
	}.OAuth2Config()
	require.NoError(t, err)
	assert.Equal(t, "test-client-id", cfg.ClientID)
	assert.Equal(t, "test-client-secret", cfg.ClientSecret)
	assert.Equal(t, "https://example.com"+config.YahooAuthCallbackPath, cfg.RedirectURL)
	assert.Equal(t, config.YahooOAuthScopes, cfg.Scopes)
	assert.Equal(t, yahoo.Endpoint, cfg.Endpoint)
}

func TestYahooAuth_OAuth2Config_NoPublicURL(t *testing.T) {
	t.Parallel()
	cfg, err := YahooAuth{ClientID: "test-client-id", ClientSecret: "test-client-secret"}.OAuth2Config()
	require.NoError(t, err)
	assert.Empty(t, cfg.RedirectURL)
}

func TestYahooAuth_OAuth2Config_MissingCredential(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		auth    YahooAuth
		wantErr string
	}{
		{name: "client id", auth: YahooAuth{ClientSecret: "secret"}, wantErr: "yahoo-oauth2-client-id is empty"},
		{name: "client secret", auth: YahooAuth{ClientID: "id"}, wantErr: "yahoo-oauth2-client-secret is empty"},
		{name: "both", auth: YahooAuth{}, wantErr: "yahoo-oauth2-client-id is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := tt.auth.OAuth2Config()
			assert.Nil(t, cfg)
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}
