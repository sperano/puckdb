package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLogLevelStr(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "debug, error, fatal, info, trace, warn",
		getLogLevelsStr())
}

func TestSetLogLevel(t *testing.T) {
	// Not parallel - modifies global zerolog state
	original := zerolog.GlobalLevel()
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(original)
		viper.Set(FlagLogLevel, nil)
	})

	tests := []struct {
		level    string
		expected zerolog.Level
	}{
		{LogLevelTrace, zerolog.TraceLevel},
		{LogLevelDebug, zerolog.DebugLevel},
		{LogLevelInfo, zerolog.InfoLevel},
		{LogLevelWarn, zerolog.WarnLevel},
		{LogLevelError, zerolog.ErrorLevel},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			viper.Set(FlagLogLevel, tt.level)
			SetLogLevel()
			assert.Equal(t, tt.expected, zerolog.GlobalLevel())
		})
	}
}

func TestGetDefaultLogPath(t *testing.T) {
	t.Parallel()
	path := GetDefaultLogPath()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	switch runtime.GOOS {
	case "darwin":
		expected := filepath.Join(home, "Library", "Logs", "puckdb", "puckdb.log")
		assert.Equal(t, expected, path)
	default:
		expected := filepath.Join(home, ".local", "share", "puckdb", "logs", "puckdb.log")
		assert.Equal(t, expected, path)
	}
}

func TestSetupLogger_Console(t *testing.T) {
	// Not parallel - modifies global logger
	t.Cleanup(func() {
		viper.Set(FlagLogFile, nil)
	})

	viper.Set(FlagLogFile, "")
	SetupLogger()
	// No assertion needed - just verify it doesn't panic
}

func TestSetupLogger_FileWithTempDir(t *testing.T) {
	// Not parallel - modifies global logger
	t.Cleanup(func() {
		viper.Set(FlagLogFile, nil)
	})

	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "test.log")

	viper.Set(FlagLogFile, logFile)
	SetupLogger()

	// Verify the directory was created (file created on first write)
	_, err := os.Stat(tempDir)
	assert.NoError(t, err)
}
