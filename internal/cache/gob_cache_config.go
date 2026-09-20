package cache

import (
	"fmt"
	"os"
	"time"

	"github.com/sperano/puckdb/internal/core"
	"gopkg.in/yaml.v3"
)

// GobCacheTypeConfig holds caching policy for a single file type.
type GobCacheTypeConfig struct {
	TTL       time.Duration `yaml:"ttl"`
	SkipRedis bool          `yaml:"skip_redis"`
}

// gobCacheConfigFile is the raw YAML structure before FileType resolution.
type gobCacheConfigFile struct {
	DefaultTTL time.Duration                 `yaml:"default_ttl"`
	Types      map[string]GobCacheTypeConfig `yaml:"types"`
}

// GobCacheConfig holds per-file-type caching policies.
type GobCacheConfig struct {
	DefaultTTL time.Duration
	Types      map[core.FileType]GobCacheTypeConfig
}

// LoadGobCacheConfig reads and validates a YAML config file.
// Returns an error if the file cannot be read, parsed, or contains unknown file types.
func LoadGobCacheConfig(path string) (*GobCacheConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gob cache config: %w", err)
	}

	var raw gobCacheConfigFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse gob cache config: %w", err)
	}

	cfg := &GobCacheConfig{
		DefaultTTL: raw.DefaultTTL,
		Types:      make(map[core.FileType]GobCacheTypeConfig, len(raw.Types)),
	}

	for name, typeCfg := range raw.Types {
		ft, ok := core.ParseFileType(name)
		if !ok {
			return nil, fmt.Errorf("gob cache config: unknown file type %q", name)
		}
		cfg.Types[ft] = typeCfg
	}

	return cfg, nil
}

// ttlFor returns the TTL for the given file type, falling back to the default.
func (c *GobCacheConfig) ttlFor(ft core.FileType) time.Duration {
	if c == nil {
		return 0
	}
	if tc, ok := c.Types[ft]; ok && tc.TTL > 0 {
		return tc.TTL
	}
	return c.DefaultTTL
}

// shouldCache returns whether the given file type should be cached in Redis.
func (c *GobCacheConfig) shouldCache(ft core.FileType) bool {
	if c == nil {
		return true
	}
	if tc, ok := c.Types[ft]; ok {
		return !tc.SkipRedis
	}
	return true
}
