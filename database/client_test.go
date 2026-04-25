package database

import (
	"testing"

	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestGetDSN(t *testing.T) {
	s := getDSN("a", "b", "c", "d", 44, "e", "f")
	assert.Equal(t, "host=a user=b password=c dbname=d port=44 sslmode=e timezone=f", s)
}

func TestGetDatabaseURL(t *testing.T) {
	// Save original values
	origHost := viper.GetString(config.FlagPostgresHost)
	origUser := viper.GetString(config.FlagPostgresUser)
	origPass := viper.GetString(config.FlagPostgresPassword)
	origPort := viper.GetInt(config.FlagPostgresPort)
	origDB := viper.GetString(config.FlagPostgresDatabase)
	origSSL := viper.GetString(config.FlagPostgresSSLMode)

	// Set test values
	viper.Set(config.FlagPostgresHost, "testhost")
	viper.Set(config.FlagPostgresUser, "testuser")
	viper.Set(config.FlagPostgresPassword, "testpass")
	viper.Set(config.FlagPostgresPort, 5433)
	viper.Set(config.FlagPostgresDatabase, "testdb")
	viper.Set(config.FlagPostgresSSLMode, "require")

	defer func() {
		// Restore original values
		viper.Set(config.FlagPostgresHost, origHost)
		viper.Set(config.FlagPostgresUser, origUser)
		viper.Set(config.FlagPostgresPassword, origPass)
		viper.Set(config.FlagPostgresPort, origPort)
		viper.Set(config.FlagPostgresDatabase, origDB)
		viper.Set(config.FlagPostgresSSLMode, origSSL)
	}()

	url := GetDatabaseURL()
	expected := "postgres://testuser:testpass@testhost:5433/testdb?sslmode=require"
	assert.Equal(t, expected, url)
}

