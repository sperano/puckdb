package config

// Redis key format strings
const (
	RedisKeyYahooTokenFmt    = "%s_yahoo_oauth2_token"
	RedisKeyYahooAuthCodeFmt = "yahoo_oauth2_code_%s"
)

// HTTP route paths
const (
	YahooAuthCallbackPath = "/yahoo/authenticated"
	YahooLandedPath       = "/yahoo/landed"
	YahooLoginPath        = "/yahoo/login"
)
