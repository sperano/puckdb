package maurice

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigDir  = ".puckdb"
	DefaultConfigFile = "maurice.yaml"
)

// Config holds Maurice REPL configuration loaded from a YAML file.
type Config struct {
	MCPServers []MCPServerConfig `yaml:"mcp_servers"`
}

// MCPServerConfig defines a single MCP server connection.
type MCPServerConfig struct {
	Name  string   `yaml:"name"`
	URL   string   `yaml:"url"`
	Tools []string `yaml:"tools,omitempty"` // whitelist; empty = all tools
}

// DefaultConfigPath returns ~/.puckdb/maurice.yaml.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, DefaultConfigDir, DefaultConfigFile)
}

// LoadConfig reads a Maurice config file from the given path.
// Returns an empty Config (no error) if the file does not exist.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read maurice config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse maurice config %s: %w", path, err)
	}
	return &cfg, nil
}
