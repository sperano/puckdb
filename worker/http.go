package worker

import (
	"context"
	"io/ioutil"
	"net/http"

	"github.com/ericsperano/yfh/core"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
)

type HTTPClient struct {
	Client *http.Client
}

func NewHTTPClient(ctx context.Context, yfh *core.YFH) (*HTTPClient, error) {
	conf, err := core.GetOauthConfig()
	if err != nil {
		return nil, err
	}
	token, err := core.LoadToken(ctx, yfh)
	if err != nil {
		return nil, err
	}
	tokenSource := conf.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, err
	}
	if newToken.AccessToken != token.AccessToken {
		if err := core.SaveToken(ctx, yfh, newToken); err != nil {
			return nil, err
		}
	}
	return &HTTPClient{oauth2.NewClient(ctx, tokenSource)}, nil
}

func (c *HTTPClient) Download(url string) ([]byte, error) {
	log.Info().Str("url", url).Msg("Downloading")
	resp, err := c.Client.Get(url)
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	log.Debug().Msg(string(body))
	return body, nil
}
