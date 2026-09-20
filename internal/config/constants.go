package config

// Redis key format strings
const (
	RedisKeyYahooTokenFmt            = "%s_yahoo_oauth2_token"
	RedisKeyYahooTokenRefreshLockFmt = "%s_yahoo_oauth2_token_refresh_lock"
	RedisKeyYahooAuthCodeFmt         = "yahoo_oauth2_code_%s"
	RedisKeyYahooLoginStateFmt       = "yahoo_oauth2_state_%s"
)

// YahooLoginStateCookie carries the per-login OAuth2 state between the login
// redirect and the callback so the callback can be bound to the browser that
// started it.
const YahooLoginStateCookie = "puckdb_yahoo_login_state"

// HTTP route paths
const (
	YahooAuthCallbackPath = "/yahoo/authenticated"
	YahooLandedPath       = "/yahoo/landed"
	YahooLoginPath        = "/yahoo/login"
)
