package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupViperInDir runs SetupViper from dir and restores viper and the
// process environment afterwards. Not parallel-safe: SetupViper mutates
// global viper state, the working directory, and the process environment.
func setupViperInDir(t *testing.T, dir string, envFile string, cleanupVars ...string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EnvFileName), []byte(envFile), 0o600))
	t.Chdir(dir)
	t.Cleanup(viper.Reset)
	for _, name := range cleanupVars {
		if _, exists := os.LookupEnv(name); !exists {
			t.Cleanup(func() { os.Unsetenv(name) })
		}
	}
	SetupViper()
}

func TestSetupViper_EnvFileResolvesHyphenatedFlags(t *testing.T) {
	setupViperInDir(t, t.TempDir(),
		"ADMIN_TOKEN=secret-from-env-file\nAPI_SERVER_ADDR=https://api.example.com\nTHEME=dark\n",
		"PUCKDB_ADMIN_TOKEN", "PUCKDB_API_SERVER_ADDR", "PUCKDB_THEME")

	assert.Equal(t, "secret-from-env-file", viper.GetString(FlagAdminToken))
	assert.Equal(t, "https://api.example.com", viper.GetString(FlagAPIServerAddr))
	assert.Equal(t, "dark", viper.GetString(FlagTheme))
}

func TestSetupViper_EnvFileAcceptsAlreadyPrefixedKeys(t *testing.T) {
	setupViperInDir(t, t.TempDir(),
		"PUCKDB_ADMIN_TOKEN=already-prefixed\n",
		"PUCKDB_ADMIN_TOKEN")

	assert.Equal(t, "already-prefixed", viper.GetString(FlagAdminToken))
}

func TestSetupViper_RealEnvWinsOverEnvFile(t *testing.T) {
	t.Setenv("PUCKDB_ADMIN_TOKEN", "from-real-env")
	setupViperInDir(t, t.TempDir(), "ADMIN_TOKEN=from-env-file\n")

	assert.Equal(t, "from-real-env", viper.GetString(FlagAdminToken))
}

func TestSetupViper_MissingEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Cleanup(viper.Reset)
	SetupViper()

	assert.Empty(t, viper.GetString(FlagAdminToken))
}
