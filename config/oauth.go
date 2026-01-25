package config

import (
	"fmt"

	"github.com/rs/zerolog/log"
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
	publicURL := viper.GetString(FlagPublicURL)
	if len(publicURL) == 0 {
		return nil, empty(FlagPublicURL)
	}
	redirectURL := fmt.Sprintf("%s/yahoo/authenticated", publicURL)
	log.Debug().Msgf("OAuth2 Redirect URL: %s", redirectURL)
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"openid", "fspt-r"}, // TODO options flags
		Endpoint:     yahoo.Endpoint,
		RedirectURL:  redirectURL,
	}, nil
}
