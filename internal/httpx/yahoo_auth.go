package httpx

import (
	"fmt"

	"github.com/sperano/puckdb/internal/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/yahoo"
)

// YahooAuth holds the Yahoo OAuth2 application credentials and the public
// URL Yahoo redirects back to. The command layer reads them from their
// flags. They are checked when used, not when built, so a process without
// Yahoo credentials starts and only fails once a Yahoo call needs them.
type YahooAuth struct {
	ClientID     string
	ClientSecret string
	// PublicURL is this server's external base URL; empty outside the
	// login flow, which is the only part that needs it.
	PublicURL string
}

// OAuth2Config returns the Yahoo OAuth2 configuration, or an error naming
// the missing credential's flag.
func (a YahooAuth) OAuth2Config() (*oauth2.Config, error) {
	if a.ClientID == "" {
		return nil, fmt.Errorf("%s is empty", config.FlagYahooOAuth2ClientID)
	}
	if a.ClientSecret == "" {
		return nil, fmt.Errorf("%s is empty", config.FlagYahooOAuth2ClientSecret)
	}
	var redirectURL string
	if a.PublicURL != "" {
		redirectURL = a.PublicURL + config.YahooAuthCallbackPath
	}
	return &oauth2.Config{
		ClientID:     a.ClientID,
		ClientSecret: a.ClientSecret,
		Scopes:       config.YahooOAuthScopes,
		Endpoint:     yahoo.Endpoint,
		RedirectURL:  redirectURL,
	}, nil
}
