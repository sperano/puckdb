package cmd

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/sperano/yfh/config"
	"github.com/stretchr/testify/assert"
)

func TestCmdInfo(t *testing.T) {
	t.Parallel()
	cmd := cmdInfo()
	b := bytes.NewBufferString("")
	cmd.SetOut(b)
	cmd.SetArgs([]string{
		"--" + config.FlagAPIPort, "123",
		"--" + config.FlagAPITLSEnabled,
		"--" + config.FlagDataPath, "aaa",
		"--" + config.FlagMetricsPort, "456",
		"--" + config.FlagMetricsRefreshInterval, "7",
		"--" + config.FlagMetricsTLSEnabled,
		"--" + config.FlagPostgresDatabase, "dbz",
		"--" + config.FlagPostgresPassword, "mypass",
		"--" + config.FlagPostgresPort, "11",
		"--" + config.FlagPostgresUser, "dbuzer",
		"--" + config.FlagSeasons, "../test-data/config/seasons.yaml",
		"--" + config.FlagRedisPassword, "otherpass",
		"--" + config.FlagRedisDB, "23",
		"--" + config.FlagRedisURL, "redisurl",
		"--" + config.FlagTLSCertificate, "tlscert",
		"--" + config.FlagTLSKey, "tlskey",
		"--" + config.FlagYahooOAuth2ClientID, "oaclient",
		"--" + config.FlagYahooHostname, "yahoohost",
		"--" + config.FlagYahooOAuth2ClientSecret, "oasecret",
		"--" + config.FlagWorkerPort, "987",
		"--" + config.FlagWorkerTLSEnabled,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(b)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"API Port:                      123",
		"API TLS Enabled:               true",
		"Data Path:                     aaa",
		"Log Level:",
		"Metrics Port:                  456",
		"Metrics Refresh Interval:      7",
		"Metrics TLS Enabled:           true",
		"Postgres Database:             dbz",
		"Postgres Password:             ******",
		"Postgres Port:                 11",
		"Postgres User:                 dbuzer",
		"Redis DB:                      23",
		"Redis Password:                *********",
		"Redis URL:                     redisurl",
		"Seasons:                       ../test-data/config/seasons.yaml",
		"Temporal Host/Port:            localhost:7233",
		"Temporal Namespace:            yfh",
		"Temporal Retry Initial:        5s",
		"Temporal Retry Max Attempts:   3",
		"TLS Certificate:               tlscert",
		"TLS Key:                       tlskey",
		"Yahoo! Oauth2 Client ID:       oaclient",
		"Yahoo! Hostname:               yahoohost",
		"Yahoo! Oauth2 Client Secret:   ********",
		"Worker Port:                   987",
		"Worker TLS Enabled:            true",
	}

	// Strip ANSI color codes
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	lines := strings.Split(ansiRegex.ReplaceAllString(string(out), ""), "\n")

	assert.Equal(t, len(expected), len(lines)-1)
	for i, line := range lines {
		if len(line) > 0 {
			// Find "INF " and take everything after it
			j := strings.Index(line, "INF ")
			if j >= 0 {
				line = strings.TrimSpace(line[j+4:])
			}
			assert.Equal(t, expected[i], line)
		}
	}
}
