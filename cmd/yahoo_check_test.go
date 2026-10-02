package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// An unusable email configuration fails before Yahoo is called, so a
// misconfigured job does not run the check and then lose its report.
func TestRunYahooCheckAccess_RejectsBadEmailConfigBeforeChecking(t *testing.T) {
	tests := map[string]map[string]any{
		"no smtp host": {
			config.FlagNotifyEmailTo: "owner@example.com",
			config.FlagSMTPFrom:      "puckdb@example.com",
			config.FlagSMTPPort:      config.DefaultSMTPPort,
		},
		"bad sender": {
			config.FlagNotifyEmailTo: "owner@example.com",
			config.FlagSMTPHost:      "smtp.example.com",
			config.FlagSMTPPort:      config.DefaultSMTPPort,
			config.FlagSMTPFrom:      "not an address",
		},
	}
	for name, settings := range tests {
		t.Run(name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			for key, value := range settings {
				viper.Set(key, value)
			}
			var out bytes.Buffer
			err := runYahooCheckAccess(context.Background(), &out)
			require.ErrorContains(t, err, "notification email")
			require.Empty(t, out.String())
		})
	}
}

// The cached game key fallback is off without a data path, and a data path
// that is not a directory is refused.
func TestCachedDataStorage(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	storage, err := cachedDataStorage()
	require.NoError(t, err)
	require.Nil(t, storage)

	dir := t.TempDir()
	viper.Set(config.FlagDataPath, dir)
	storage, err = cachedDataStorage()
	require.NoError(t, err)
	require.NotNil(t, storage)

	viper.Set(config.FlagDataPath, filepath.Join(dir, "not-mounted"))
	_, err = cachedDataStorage()
	require.ErrorContains(t, err, "data path")

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	viper.Set(config.FlagDataPath, file)
	_, err = cachedDataStorage()
	require.ErrorContains(t, err, "not a directory")
}
