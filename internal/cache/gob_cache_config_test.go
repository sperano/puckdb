package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGobCacheConfig(t *testing.T) {
	t.Parallel()

	yaml := `
default_ttl: 30m
types:
  Boxscore:
    ttl: 2h
  PlayByPlay:
    skip_redis: true
  DailySchedule:
    ttl: 5m
    skip_redis: false
`
	path := writeTestConfig(t, yaml)

	cfg, err := LoadGobCacheConfig(path)
	require.NoError(t, err)

	assert.Equal(t, 30*time.Minute, cfg.DefaultTTL)
	assert.Len(t, cfg.Types, 3)

	assert.Equal(t, 2*time.Hour, cfg.Types[core.Boxscore].TTL)
	assert.False(t, cfg.Types[core.Boxscore].SkipRedis)

	assert.True(t, cfg.Types[core.PlayByPlay].SkipRedis)

	assert.Equal(t, 5*time.Minute, cfg.Types[core.DailySchedule].TTL)
	assert.False(t, cfg.Types[core.DailySchedule].SkipRedis)
}

func TestLoadGobCacheConfig_UnknownFileType(t *testing.T) {
	t.Parallel()

	yaml := `
default_ttl: 30m
types:
  NotARealType:
    ttl: 5m
`
	path := writeTestConfig(t, yaml)

	_, err := LoadGobCacheConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown file type")
	assert.Contains(t, err.Error(), "NotARealType")
}

func TestLoadGobCacheConfig_FileNotFound(t *testing.T) {
	t.Parallel()

	_, err := LoadGobCacheConfig("/nonexistent/path.yaml")
	require.Error(t, err)
}

func TestLoadGobCacheConfig_InvalidYAML(t *testing.T) {
	t.Parallel()

	path := writeTestConfig(t, ":::invalid")

	_, err := LoadGobCacheConfig(path)
	require.Error(t, err)
}

func TestGobCacheConfig_TTLFor(t *testing.T) {
	t.Parallel()

	cfg := &GobCacheConfig{
		DefaultTTL: 30 * time.Minute,
		Types: map[core.FileType]GobCacheTypeConfig{
			core.Boxscore: {TTL: 2 * time.Hour},
		},
	}

	assert.Equal(t, 2*time.Hour, cfg.ttlFor(core.Boxscore))
	assert.Equal(t, 30*time.Minute, cfg.ttlFor(core.PlayByPlay))
}

func TestGobCacheConfig_TTLFor_Nil(t *testing.T) {
	t.Parallel()

	var cfg *GobCacheConfig
	assert.Equal(t, time.Duration(0), cfg.ttlFor(core.Boxscore))
}

func TestGobCacheConfig_ShouldCache(t *testing.T) {
	t.Parallel()

	cfg := &GobCacheConfig{
		Types: map[core.FileType]GobCacheTypeConfig{
			core.PlayByPlay: {SkipRedis: true},
			core.Boxscore:   {TTL: 2 * time.Hour},
		},
	}

	assert.False(t, cfg.shouldCache(core.PlayByPlay))
	assert.True(t, cfg.shouldCache(core.Boxscore))
	assert.True(t, cfg.shouldCache(core.DailySchedule))
}

func TestGobCacheConfig_ShouldCache_Nil(t *testing.T) {
	t.Parallel()

	var cfg *GobCacheConfig
	assert.True(t, cfg.shouldCache(core.Boxscore))
}

func TestGobCache_ResolveTTL_WithConfig(t *testing.T) {
	t.Parallel()

	client, _ := redismock.NewClientMock()
	cfg := &GobCacheConfig{
		DefaultTTL: 30 * time.Minute,
		Types: map[core.FileType]GobCacheTypeConfig{
			core.Boxscore: {TTL: 2 * time.Hour},
		},
	}
	c := NewGobCache(client, WithConfig(cfg))

	assert.Equal(t, 2*time.Hour, c.resolveTTL(core.Boxscore))
	assert.Equal(t, 30*time.Minute, c.resolveTTL(core.PlayByPlay))
}

func TestGobCache_ResolveTTL_WithoutConfig(t *testing.T) {
	t.Parallel()

	client, _ := redismock.NewClientMock()
	c := NewGobCache(client, WithTTL(45*time.Minute))

	assert.Equal(t, 45*time.Minute, c.resolveTTL(core.Boxscore))
}

func TestGobCache_ResolveTTL_NilCache(t *testing.T) {
	t.Parallel()

	var c *GobCache
	assert.Equal(t, GobCacheTTL, c.resolveTTL(core.Boxscore))
}

func TestGobCache_ShouldCache_WithConfig(t *testing.T) {
	t.Parallel()

	client, _ := redismock.NewClientMock()
	cfg := &GobCacheConfig{
		Types: map[core.FileType]GobCacheTypeConfig{
			core.PlayByPlay: {SkipRedis: true},
		},
	}
	c := NewGobCache(client, WithConfig(cfg))

	assert.False(t, c.shouldCache(core.PlayByPlay))
	assert.True(t, c.shouldCache(core.Boxscore))
}

func TestGobCache_ShouldCache_NilCache(t *testing.T) {
	t.Parallel()

	var c *GobCache
	assert.True(t, c.shouldCache(core.PlayByPlay))
}

func TestNewGobCacheWithConfig(t *testing.T) {
	t.Parallel()

	client, _ := redismock.NewClientMock()

	t.Run("uses config default_ttl", func(t *testing.T) {
		cfg := &GobCacheConfig{DefaultTTL: 15 * time.Minute}
		c := NewGobCache(client, WithConfig(cfg))
		assert.Equal(t, 15*time.Minute, c.ttl)
		assert.Equal(t, cfg, c.config)
	})

	t.Run("falls back to constant when no default_ttl", func(t *testing.T) {
		cfg := &GobCacheConfig{}
		c := NewGobCache(client, WithConfig(cfg))
		assert.Equal(t, GobCacheTTL, c.ttl)
	})

	t.Run("nil config uses constant", func(t *testing.T) {
		c := NewGobCache(client, WithConfig(nil))
		assert.Equal(t, GobCacheTTL, c.ttl)
		assert.Nil(t, c.config)
	})
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gob-cache.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}
