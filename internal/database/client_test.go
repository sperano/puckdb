package database

import (
	"context"
	"math"
	"net/url"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPoolOptions(t *testing.T) {
	t.Parallel()
	assert.Equal(t, PoolOptions{MaxConns: 5, MinConns: 2, MaxConnLifetime: 5 * time.Minute}, DefaultPoolOptions())
	require.NoError(t, DefaultPoolOptions().Validate())
}

func TestPoolOptionsValidate(t *testing.T) {
	t.Parallel()
	const lifetime = time.Minute
	tests := []struct {
		name     string
		opts     PoolOptions
		wantFlag string
	}{
		{name: "single connection", opts: PoolOptions{MaxConns: 1, MinConns: 1, MaxConnLifetime: lifetime}},
		{name: "no minimum", opts: PoolOptions{MaxConns: 1, MaxConnLifetime: lifetime}},
		{name: "no connections", opts: PoolOptions{MinConns: 0, MaxConnLifetime: lifetime}, wantFlag: config.FlagPostgresMaxOpenConns},
		{name: "negative maximum", opts: PoolOptions{MaxConns: -1}, wantFlag: config.FlagPostgresMaxOpenConns},
		{name: "maximum overflows int32", opts: PoolOptions{MaxConns: math.MaxInt32 + 1}, wantFlag: config.FlagPostgresMaxOpenConns},
		{name: "minimum above maximum", opts: PoolOptions{MaxConns: 1, MinConns: 2}, wantFlag: config.FlagPostgresMaxIdleConns},
		{name: "negative minimum", opts: PoolOptions{MaxConns: 1, MinConns: -1}, wantFlag: config.FlagPostgresMaxIdleConns},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.opts.Validate()
			if tt.wantFlag == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrInvalidPoolOptions)
			assert.Contains(t, err.Error(), "--"+tt.wantFlag)
		})
	}
}

func TestPoolConfig_AppliesPoolOptions(t *testing.T) {
	t.Parallel()
	const (
		maxConns = 7
		minConns = 3
		lifetime = 90 * time.Second
	)
	got, err := poolConfig(Config{
		Conn: testConnConfig("db.example", "secret"),
		Pool: PoolOptions{MaxConns: maxConns, MinConns: minConns, MaxConnLifetime: lifetime},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(maxConns), got.MaxConns)
	assert.Equal(t, int32(minConns), got.MinConns)
	assert.Equal(t, lifetime, got.MaxConnLifetime)
	assert.Equal(t, "db.example", got.ConnConfig.Host)
	assert.Equal(t, uint16(testPort), got.ConnConfig.Port)
}

func TestOpenPGXPool_RejectsInvalidPoolOptionsBeforeConnecting(t *testing.T) {
	t.Parallel()
	pool, err := OpenPGXPool(context.Background(), Config{
		Conn: testConnConfig("localhost", "secret"),
		Pool: PoolOptions{MaxConns: 1, MinConns: 2},
	})
	require.ErrorIs(t, err, ErrInvalidPoolOptions)
	assert.Nil(t, pool)
}

func TestOpenPGXPool_InvalidConfig(t *testing.T) {
	t.Parallel()
	// pgconn rejects ports outside 1-65535 during ParseConfig, before any
	// network call. This exercises the parse-error branch in OpenPGXPool
	// without needing a real database.
	const (
		secretPassword = "s3cr3t-p@ss"
		outOfRangePort = 99999
	)
	conn := ConnConfig{
		Host: "localhost", Port: outOfRangePort, User: "u", Password: secretPassword,
		Database: "d", SSLMode: "disable", TimeZone: "UTC",
	}
	pool, err := OpenPGXPool(context.Background(), Config{Conn: conn, Pool: DefaultPoolOptions()})
	require.Error(t, err)
	assert.Nil(t, pool)
	assert.NotContains(t, err.Error(), secretPassword)
	assert.NotContains(t, err.Error(), url.QueryEscape(secretPassword))
}
