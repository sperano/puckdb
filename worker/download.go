package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/yfh/auth"
	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/http"
	"github.com/sperano/yfh/redis"
)

func DownloadFromYahoo(url string) ([]byte, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return downloadFromYahooImpl(redisClient, url)
}

// downloadFromYahooImpl is the testable implementation.
func downloadFromYahooImpl(redisClient redis.Client, url string) ([]byte, error) {
	ctx := context.WithValue(context.Background(), auth.CtxUser, config.DefaultUser)
	return http.DownloadYahoo(ctx, redisClient, url)
}

func DownloadBoxscore(gameid nhl.GameID) ([]byte, error) {
	log.Info().Str("gameid", gameid.String()).Msg("Downloading boxscore NHL API")
	client := nhl.NewClient()
	boxscore, err := client.Boxscore(context.Background(), gameid)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(boxscore)
	if err != nil {
		return nil, fmt.Errorf("gameid %s: %w", gameid, err)
	}
	return data, nil
}
