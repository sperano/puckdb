package config

import (
	"fmt"

	"github.com/spf13/viper"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/yahoo"
)

func OauthConfig() (*oauth2.Config, error) {
	empty := func(s string) error {
		return fmt.Errorf("%s is empty", s)
	}
	clientID := viper.GetString(FlagYahooOAuth2ClientID)
	if len(clientID) == 0 {
		return nil, empty(FlagYahooOAuth2ClientID)
	}
	clientSecret := viper.GetString(FlagYahooOAuth2ClientSecret)
	if len(clientSecret) == 0 {
		return nil, empty(FlagYahooOAuth2ClientSecret)
	}
	// public-url is only needed for OAuth login flow, not for using existing tokens
	var redirectURL string
	if publicURL := viper.GetString(FlagPublicURL); len(publicURL) > 0 {
		redirectURL = fmt.Sprintf("%s/yahoo/authenticated", publicURL)
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"openid", "fspt-r"}, // TODO options flags
		Endpoint:     yahoo.Endpoint,
		RedirectURL:  redirectURL,
	}, nil
}
