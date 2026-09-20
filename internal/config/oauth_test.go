package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOauthConfig(t *testing.T) {
	cleanup := func() {
		viper.Set(FlagYahooOAuth2ClientID, nil)
		viper.Set(FlagYahooOAuth2ClientSecret, nil)
		viper.Set(FlagPublicURL, nil)
	}
	t.Cleanup(cleanup)

	viper.Set(FlagYahooOAuth2ClientID, "test-client-id")
	viper.Set(FlagYahooOAuth2ClientSecret, "test-client-secret")
	viper.Set(FlagPublicURL, "https://example.com")

	cfg, err := OauthConfig()
	require.NoError(t, err)
	assert.Equal(t, "test-client-id", cfg.ClientID)
	assert.Equal(t, "test-client-secret", cfg.ClientSecret)
	assert.Equal(t, "https://example.com"+YahooAuthCallbackPath, cfg.RedirectURL)
	assert.Equal(t, YahooOAuthScopes, cfg.Scopes)
}

func TestOauthConfig_MissingClientID(t *testing.T) {
	cleanup := func() {
		viper.Set(FlagYahooOAuth2ClientID, nil)
		viper.Set(FlagYahooOAuth2ClientSecret, nil)
	}
	t.Cleanup(cleanup)

	viper.Set(FlagYahooOAuth2ClientID, "")
	viper.Set(FlagYahooOAuth2ClientSecret, "test-client-secret")

	cfg, err := OauthConfig()
	assert.Nil(t, cfg)
	assert.EqualError(t, err, FlagYahooOAuth2ClientID+" is empty")
}

func TestOauthConfig_MissingClientSecret(t *testing.T) {
	cleanup := func() {
		viper.Set(FlagYahooOAuth2ClientID, nil)
		viper.Set(FlagYahooOAuth2ClientSecret, nil)
	}
	t.Cleanup(cleanup)

	viper.Set(FlagYahooOAuth2ClientID, "test-client-id")
	viper.Set(FlagYahooOAuth2ClientSecret, "")

	cfg, err := OauthConfig()
	assert.Nil(t, cfg)
	assert.EqualError(t, err, FlagYahooOAuth2ClientSecret+" is empty")
}

func TestOauthConfig_NoPublicURL(t *testing.T) {
	cleanup := func() {
		viper.Set(FlagYahooOAuth2ClientID, nil)
		viper.Set(FlagYahooOAuth2ClientSecret, nil)
		viper.Set(FlagPublicURL, nil)
	}
	t.Cleanup(cleanup)

	viper.Set(FlagYahooOAuth2ClientID, "test-client-id")
	viper.Set(FlagYahooOAuth2ClientSecret, "test-client-secret")
	viper.Set(FlagPublicURL, "")

	cfg, err := OauthConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.RedirectURL)
}
