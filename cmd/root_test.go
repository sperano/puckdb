package cmd

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// TestBindFlagsReturnsErrorOnInvalidValue verifies that BindFlags surfaces a
// flags.Set failure as a returned error instead of calling log.Fatal (which
// would os.Exit and bypass every deferred cleanup in the calling command).
func TestBindFlagsReturnsErrorOnInvalidValue(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Int("test-int", 0, "an int flag")

	// VisitAll maps dashes to underscores before consulting viper.
	viper.Set("test_int", "not-a-number")

	err := BindFlags(fs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "test-int")
}

// TestBindFlagsAppliesViperValue verifies the happy path: an unset flag with a
// valid viper value is applied and no error is returned.
func TestBindFlagsAppliesViperValue(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Int("test-int", 0, "an int flag")

	viper.Set("test_int", 42)

	require.NoError(t, BindFlags(fs))

	got, err := fs.GetInt("test-int")
	require.NoError(t, err)
	require.Equal(t, 42, got)
}

// TestBindFlagsSkipsChangedFlag verifies an explicitly-set (Changed) flag is
// left untouched even when viper holds a different value.
func TestBindFlagsSkipsChangedFlag(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Int("test-int", 0, "an int flag")
	require.NoError(t, fs.Set("test-int", "7"))

	viper.Set("test_int", 99)

	require.NoError(t, BindFlags(fs))

	got, err := fs.GetInt("test-int")
	require.NoError(t, err)
	require.Equal(t, 7, got)
}
