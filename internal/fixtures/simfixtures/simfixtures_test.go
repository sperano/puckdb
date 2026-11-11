package simfixtures

import (
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseURLNamesTheNewDatabase(t *testing.T) {
	const name = "puckdb_test_sim_x"
	for _, baseURL := range []string{
		"postgres://puckdb:foo@localhost:5432/puckdb_test?sslmode=disable",
		"postgres://puckdb:foo@localhost:5432/other?dbname=puckdb_test&sslmode=disable",
	} {
		t.Run(baseURL, func(t *testing.T) {
			dbURL, err := databaseURL(baseURL, name)
			require.NoError(t, err)
			cfg, err := pgconn.ParseConfig(dbURL)
			require.NoError(t, err)
			assert.Equal(t, name, cfg.Database)
			parsed, err := url.Parse(dbURL)
			require.NoError(t, err)
			assert.Equal(t, "disable", parsed.Query().Get("sslmode"), "other parameters are kept")
		})
	}
}
