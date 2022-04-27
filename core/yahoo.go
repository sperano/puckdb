package core

import (
	"context"
	"encoding/json"
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/yahoo"
)

const BaseAPIURL = "https://fantasysports.yahooapis.com/fantasy/v2"

const BaseSportsURL = "https://sports.yahoo.com"

// https://sports.yahoo.com/nhl/scoreboard/?confId=&dateRange=2022-4-9&schedState=2

const FlagYahooOAuth2ClientID = "yahoo_oauth2_client_id"
const FlagYahooOAuth2ClientSecret = "yahoo_oauth2_client_secret"
const FlagYahooOAuth2ClientRedirect = "yahoo_oauth2_client_redirect"

func GetOauthConfig() (*oauth2.Config, error) {
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
	clientRedirect := viper.GetString(FlagYahooOAuth2ClientRedirect)
	if len(clientRedirect) == 0 {
		return nil, empty(FlagYahooOAuth2ClientRedirect)
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"openid", "fspt-w"},
		Endpoint:     yahoo.Endpoint,
		RedirectURL:  clientRedirect,
		//RedirectURL:  "oob",
	}, nil
}

func TokenAsString(token *oauth2.Token) (string, error) {
	b, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func getRedisKeyForToken(user string) string {
	return fmt.Sprintf("%s_yahoo_oauth2_token", user)
}

func SaveToken(ctx context.Context, yfh *YFH, token *oauth2.Token) error {
	s, err := TokenAsString(token)
	if err != nil {
		return err
	}
	key := getRedisKeyForToken(GetCtxUser(ctx))
	log.Infof("Saving token for %s: %s", key, s)
	return yfh.RedisClient.Set(ctx, key, s, 0).Err()
}

func LoadToken(ctx context.Context, yfh *YFH) (*oauth2.Token, error) {
	key := getRedisKeyForToken(GetCtxUser(ctx))
	log.Infof("Loading token for %s", key)
	status := yfh.RedisClient.Get(ctx, key)
	if err := status.Err(); err != nil {
		return nil, err
	}
	tokenAsString, err := status.Result()
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal([]byte(tokenAsString), &token); err != nil {
		return nil, err
	}
	return &token, nil
}
