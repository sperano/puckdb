package database

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPort     = 6543
	testSSLMode  = "require"
	testTimeZone = "America/Toronto"
	ipv6Loopback = "::1"
)

// testHosts covers a DNS name, IPv4, and both short and long IPv6 literals.
var testHosts = []string{"db.example.com", "192.0.2.10", ipv6Loopback, "2001:db8::5"}

// hostileValues are credentials and names containing URL delimiters and
// escapes that a naive fmt.Sprintf URL would corrupt.
var hostileValues = []string{
	"plain",
	"pct%41value",
	"hash#frag",
	"question?mark=1",
	"slash/es//here",
	"with space s",
	"at@colon:semi;amp&plus+",
	`quote'back\slash`,
	"all %#?/ @:together",
}

func testConnConfig(host, secret string) ConnConfig {
	return ConnConfig{
		Host:     host,
		Port:     testPort,
		User:     "user-" + secret,
		Password: secret,
		Database: "db-" + secret,
		SSLMode:  testSSLMode,
		TimeZone: testTimeZone,
	}
}

func TestConnConfig_URL_RoundTripsThroughPgx(t *testing.T) {
	t.Parallel()
	for _, host := range testHosts {
		for _, secret := range hostileValues {
			t.Run(host+"/"+secret, func(t *testing.T) {
				t.Parallel()
				cfg := testConnConfig(host, secret)

				connURL, err := cfg.URL()
				require.NoError(t, err)

				parsed, err := pgxpool.ParseConfig(connURL)
				require.NoError(t, err)

				conn := parsed.ConnConfig
				assert.Equal(t, cfg.Host, conn.Host)
				assert.Equal(t, uint16(cfg.Port), conn.Port)
				assert.Equal(t, cfg.User, conn.User)
				assert.Equal(t, cfg.Password, conn.Password)
				assert.Equal(t, cfg.Database, conn.Database)
				assert.Equal(t, cfg.TimeZone, conn.RuntimeParams[queryParamTimeZone])
				assert.NotNil(t, conn.TLSConfig, "sslmode=require must enable TLS")
			})
		}
	}
}

// The migration runner connects through lib/pq, not pgx, so the same URL
// must decode to the same settings there too.
func TestConnConfig_URL_RoundTripsThroughLibPQ(t *testing.T) {
	t.Parallel()
	for _, host := range testHosts {
		for _, secret := range hostileValues {
			t.Run(host+"/"+secret, func(t *testing.T) {
				t.Parallel()
				cfg := testConnConfig(host, secret)

				connURL, err := cfg.URL()
				require.NoError(t, err)

				// pq.ParseURL yields the keyword/value DSN lib/pq connects
				// with; pgconn parses that format, so compare settings
				// rather than pq's string formatting.
				dsn, err := pq.ParseURL(connURL)
				require.NoError(t, err)
				parsed, err := pgconn.ParseConfig(dsn)
				require.NoError(t, err)

				assert.Equal(t, cfg.Host, parsed.Host)
				assert.Equal(t, uint16(cfg.Port), parsed.Port)
				assert.Equal(t, cfg.User, parsed.User)
				assert.Equal(t, cfg.Password, parsed.Password)
				assert.Equal(t, cfg.Database, parsed.Database)
				assert.Equal(t, cfg.TimeZone, parsed.RuntimeParams[queryParamTimeZone])
				assert.NotNil(t, parsed.TLSConfig, "sslmode=require must enable TLS")
			})
		}
	}
}

func TestConnConfig_URL_OmitsEmptyOptions(t *testing.T) {
	t.Parallel()
	cfg := testConnConfig("localhost", "plain")
	cfg.SSLMode = ""
	cfg.TimeZone = ""

	connURL, err := cfg.URL()
	require.NoError(t, err)
	assert.NotContains(t, connURL, "?")

	parsed, err := pgxpool.ParseConfig(connURL)
	require.NoError(t, err)
	assert.NotContains(t, parsed.ConnConfig.RuntimeParams, queryParamTimeZone)
}

func TestConnConfig_FormattingRedactsPassword(t *testing.T) {
	t.Parallel()
	const secret = "hunter2-secret"
	cfg := testConnConfig("localhost", "plain")
	cfg.Password = secret

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(verb, cfg)
		assert.NotContains(t, out, secret, verb)
		assert.Contains(t, out, redactedPassword, verb)
	}
	connURL, err := cfg.URL()
	require.NoError(t, err)
	assert.Contains(t, connURL, secret, "URL must still carry the real password")
}

// unparseableHost percent-encodes to a host that net/url refuses to parse
// back, which is how a stray space in a secret value would surface.
const unparseableHost = "bad host"

// leakyPassword changes under percent-encoding, so both forms are checked.
const leakyPassword = "s3cr3t-p@ss/#x"

func assertNoPasswordLeak(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), leakyPassword)
	assert.NotContains(t, err.Error(), url.QueryEscape(leakyPassword))
	assert.NotContains(t, err.Error(), url.PathEscape(leakyPassword))
	assert.NotContains(t, err.Error(), url.UserPassword("u", leakyPassword).String())
}

func TestConnConfig_URL_RejectsUnparseableWithoutLeakingPassword(t *testing.T) {
	t.Parallel()
	cfg := testConnConfig(unparseableHost, "plain")
	cfg.Password = leakyPassword

	connURL, err := cfg.URL()
	assert.Empty(t, connURL)
	require.ErrorIs(t, err, ErrInvalidConnConfig)
	assertNoPasswordLeak(t, err)
	assert.Contains(t, err.Error(), redactedPassword)
}

// golang-migrate returns net/url parse errors verbatim, URL included, and
// does not redact. The migration entry points must fail before reaching it.
func TestMigrations_UnparseableConfigDoesNotLeakPassword(t *testing.T) {
	t.Parallel()
	conn := testConnConfig(unparseableHost, "plain")
	conn.Password = leakyPassword

	for name, run := range map[string]func() error{
		"up":    func() error { return RunSQLMigrations(conn) },
		"down":  func() error { return RunSQLMigrationsDown(conn) },
		"force": func() error { return ForceMigrationVersion(conn, NoMigrationVersion) },
		"pool": func() error {
			_, err := OpenPGXPool(context.Background(), Config{Conn: conn, Pool: DefaultPoolOptions()})
			return err
		},
	} {
		err := run()
		require.ErrorIs(t, err, ErrInvalidConnConfig, name)
		assertNoPasswordLeak(t, err)
	}
}
