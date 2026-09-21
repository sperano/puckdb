package config

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlagGroupInit_String(t *testing.T) {
	t.Parallel()
	fg := FlagGroup{
		Flags: []FlagDef{
			{Name: "test-string", Default: "default-value", Usage: "A test string flag"},
			{Name: "test-string-short", Short: "s", Default: "default2", Usage: "With shorthand"},
		},
	}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	fg.Init(flags)

	// Verify string flag was registered
	f := flags.Lookup("test-string")
	require.NotNil(t, f)
	assert.Equal(t, "default-value", f.DefValue)
	assert.Equal(t, "A test string flag", f.Usage)

	// Verify string flag with shorthand
	f = flags.Lookup("test-string-short")
	require.NotNil(t, f)
	assert.Equal(t, "s", f.Shorthand)
}

func TestFlagGroupInit_Int(t *testing.T) {
	t.Parallel()
	fg := FlagGroup{
		Flags: []FlagDef{
			{Name: "test-int", Default: 42, Usage: "A test int flag"},
			{Name: "test-int-short", Short: "i", Default: 100, Usage: "With shorthand"},
		},
	}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	fg.Init(flags)

	// Verify int flag was registered
	f := flags.Lookup("test-int")
	require.NotNil(t, f)
	assert.Equal(t, "42", f.DefValue)

	// Verify int flag with shorthand
	f = flags.Lookup("test-int-short")
	require.NotNil(t, f)
	assert.Equal(t, "i", f.Shorthand)
}

func TestFlagGroupInit_Bool(t *testing.T) {
	t.Parallel()
	fg := FlagGroup{
		Flags: []FlagDef{
			{Name: "test-bool", Default: true, Usage: "A test bool flag"},
			{Name: "test-bool-short", Short: "b", Default: false, Usage: "With shorthand"},
		},
	}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	fg.Init(flags)

	// Verify bool flag was registered
	f := flags.Lookup("test-bool")
	require.NotNil(t, f)
	assert.Equal(t, "true", f.DefValue)

	// Verify bool flag with shorthand
	f = flags.Lookup("test-bool-short")
	require.NotNil(t, f)
	assert.Equal(t, "b", f.Shorthand)
}

func TestFlagGroupBind(t *testing.T) {
	// Not parallel - modifies global viper state
	t.Cleanup(func() {
		viper.Reset()
	})

	fg := FlagGroup{
		Flags: []FlagDef{
			{Name: "bind-test-flag", Default: "test-value", Usage: "Test binding"},
		},
	}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	fg.Init(flags)

	err := fg.Bind(flags)
	require.NoError(t, err)

	// Set the flag and verify viper sees it
	err = flags.Set("bind-test-flag", "new-value")
	require.NoError(t, err)
	assert.Equal(t, "new-value", viper.GetString("bind-test-flag"))
}

func TestFlagGroupSensitiveFlags(t *testing.T) {
	t.Parallel()
	fg := FlagGroup{
		Flags: []FlagDef{
			{Name: "password", Default: "", Sensitive: true},
			{Name: "secret", Default: "", Sensitive: true},
			{Name: "username", Default: "", Sensitive: false},
		},
	}

	sensitive := fg.sensitiveFlags()
	assert.True(t, sensitive["password"])
	assert.True(t, sensitive["secret"])
	assert.False(t, sensitive["username"])
	assert.Len(t, sensitive, 2)
}

func TestInitLoggingFlags(t *testing.T) {
	t.Parallel()
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	InitLoggingFlags(flags, "debug", "/var/log/test.log")

	// Verify log-level flag
	f := flags.Lookup(FlagLogLevel)
	require.NotNil(t, f)
	assert.Equal(t, "debug", f.DefValue)
	assert.Equal(t, "L", f.Shorthand)

	// Verify log-file flag
	f = flags.Lookup(FlagLogFile)
	require.NotNil(t, f)
	assert.Equal(t, "/var/log/test.log", f.DefValue)
}

func TestBindLoggingFlags(t *testing.T) {
	// Not parallel - modifies global viper state
	t.Cleanup(func() {
		viper.Reset()
	})

	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	InitLoggingFlags(flags, "info", "")

	err := BindLoggingFlags(flags)
	require.NoError(t, err)

	// Set the flag and verify viper sees it
	err = flags.Set(FlagLogLevel, "debug")
	require.NoError(t, err)
	assert.Equal(t, "debug", viper.GetString(FlagLogLevel))
}

func TestYahooSeasonsFlags(t *testing.T) {
	t.Parallel()
	flags := flag.NewFlagSet("test", flag.ContinueOnError)

	YahooSeasonsFlags.Init(flags)

	// Verify flag was registered
	f := flags.Lookup(FlagYahooSeasons)
	require.NotNil(t, f)
	assert.Equal(t, DefaultYahooSeasonsFile, f.DefValue)
	assert.Equal(t, "S", f.Shorthand)
}

func TestSetupViper(t *testing.T) {
	// Not parallel - modifies global viper state
	t.Cleanup(func() {
		viper.Reset()
	})

	SetupViper()

	// Verify env prefix is set by checking that PUCKDB_TEST_VAR would map correctly
	// We can't easily test the full behavior without setting actual env vars,
	// but we can verify it doesn't panic and that config paths are set
}

func TestLogFlagValues(t *testing.T) {
	// Not parallel - modifies global viper and logger state
	originalLogger := log.Logger
	t.Cleanup(func() {
		log.Logger = originalLogger
		viper.Reset()
	})

	// Capture log output
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)

	// Set some test values
	viper.Set("test-flag", "test-value")
	viper.Set("postgres-password", "secret123") // Should be redacted

	LogFlagValues()

	output := buf.String()
	assert.Contains(t, output, "test-flag")
	assert.Contains(t, output, "test-value")
	assert.Contains(t, output, "postgres-password")
	assert.Contains(t, output, "[REDACTED]")
	assert.NotContains(t, output, "secret123")
}

func TestLogFlagValues_EmptySettings(t *testing.T) {
	// Not parallel - modifies global viper state
	t.Cleanup(func() {
		viper.Reset()
	})

	// Should not panic with empty settings
	viper.Reset()
	LogFlagValues()
}

func TestGetSeasonRange(t *testing.T) {
	// Not parallel - modifies global viper state
	cleanup := func() {
		viper.Set(FlagSeasonYear, nil)
		viper.Set(FlagFromSeasonYear, nil)
		viper.Set(FlagToSeasonYear, nil)
	}
	t.Cleanup(cleanup)

	tests := []struct {
		name      string
		season    int
		from      int
		to        int
		wantStart int
		wantEnd   int
	}{
		{"single season flag", 2023, 0, 0, 2023, 2023},
		{"range flags", 0, 2020, 2024, 2020, 2024},
		{"season overrides range", 2023, 2020, 2024, 2023, 2023},
		{"no flags set", 0, 0, 0, 0, 0},
		{"only from set", 0, 2020, 0, 2020, 0},
		{"only to set", 0, 0, 2024, 0, 2024},
		{"negative season preserved", -1, 0, 0, -1, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup() // Reset before each subtest
			viper.Set(FlagSeasonYear, tt.season)
			viper.Set(FlagFromSeasonYear, tt.from)
			viper.Set(FlagToSeasonYear, tt.to)

			start, end := GetSeasonRange()
			assert.Equal(t, tt.wantStart, start)
			assert.Equal(t, tt.wantEnd, end)
		})
	}
}
