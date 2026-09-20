package database

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/viper"
)

const (
	postgresURLScheme = "postgres"

	// Query parameters understood by both pgx and lib/pq (the driver behind
	// golang-migrate). sslmode is a driver setting; timezone is unknown to
	// both drivers, so each forwards it to the server as a startup runtime
	// parameter.
	queryParamSSLMode  = "sslmode"
	queryParamTimeZone = "timezone"

	redactedPassword = "REDACTED"
)

// ErrInvalidConnConfig reports connection settings that cannot form a valid
// URL, such as a host containing whitespace.
var ErrInvalidConnConfig = errors.New("invalid postgres connection settings")

// ConnConfig is the single source of PostgreSQL connection settings, shared
// by the pgx pool and the migration runner so they cannot drift apart.
type ConnConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
	TimeZone string
}

// ConnConfigFromViper reads the connection settings from the global config.
func ConnConfigFromViper() ConnConfig {
	return ConnConfig{
		Host:     viper.GetString(config.FlagPostgresHost),
		Port:     viper.GetInt(config.FlagPostgresPort),
		User:     viper.GetString(config.FlagPostgresUser),
		Password: viper.GetString(config.FlagPostgresPassword),
		Database: viper.GetString(config.FlagPostgresDatabase),
		SSLMode:  viper.GetString(config.FlagPostgresSSLMode),
		TimeZone: viper.GetString(config.FlagPostgresTimeZone),
	}
}

// URL returns the connection URL with every component percent-encoded and
// IPv6 hosts bracketed. It contains the password: never log it, use String.
//
// The URL is re-parsed here because its consumers are not equally careful:
// golang-migrate returns net/url parse errors verbatim, and those embed the
// whole URL. Rejecting an unparseable URL first keeps the password out of
// errors that end up in pod logs and workflow history.
func (c ConnConfig) URL() (string, error) {
	connURL := c.buildURL(c.Password).String()
	if _, err := url.Parse(connURL); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidConnConfig, c)
	}
	return connURL, nil
}

// String returns the connection URL with the password redacted, so the
// config is safe to pass to loggers and format verbs.
func (c ConnConfig) String() string {
	return c.buildURL(redactedPassword).String()
}

// GoString keeps %#v from bypassing the redaction in String.
func (c ConnConfig) GoString() string {
	return c.String()
}

func (c ConnConfig) buildURL(password string) *url.URL {
	query := url.Values{}
	if c.SSLMode != "" {
		query.Set(queryParamSSLMode, c.SSLMode)
	}
	if c.TimeZone != "" {
		query.Set(queryParamTimeZone, c.TimeZone)
	}
	return &url.URL{
		Scheme:   postgresURLScheme,
		User:     url.UserPassword(c.User, password),
		Host:     net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:     "/" + c.Database,
		RawQuery: query.Encode(),
	}
}
