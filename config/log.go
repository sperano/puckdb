package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Log level constants
const (
	LogLevelTrace = "trace"
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
	LogLevelFatal = "fatal"
)

var logLevels map[string]zerolog.Level

func init() {
	logLevels = make(map[string]zerolog.Level)
	logLevels[LogLevelTrace] = zerolog.TraceLevel
	logLevels[LogLevelDebug] = zerolog.DebugLevel
	logLevels[LogLevelInfo] = zerolog.InfoLevel
	logLevels[LogLevelWarn] = zerolog.WarnLevel
	logLevels[LogLevelError] = zerolog.ErrorLevel
	logLevels[LogLevelFatal] = zerolog.FatalLevel
}

func getLogLevelsStr() string {
	arr := make([]string, len(logLevels))
	i := 0
	for k := range logLevels {
		arr[i] = k
		i++
	}
	sort.Strings(arr)
	return strings.Join(arr, ", ")
}

func SetLogLevel() {
	s := viper.GetString(FlagLogLevel)
	lvl, found := logLevels[s]
	if !found {
		log.Warn().Str("invalid", s).Str("using", DefaultLogLevel).Msg("Invalid log level, using default")
		lvl = logLevels[DefaultLogLevel]
	}
	zerolog.SetGlobalLevel(lvl)
}

// GetDefaultLogPath returns the platform-specific default log file path.
func GetDefaultLogPath() string {
	var dir string
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Library", "Logs", "puckdb")
	default: // linux and others
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share", "puckdb", "logs")
	}
	return filepath.Join(dir, "puckdb.log")
}

// SetupLogger configures the global zerolog logger based on the log-file flag.
// If logFile is empty, logs go to console only.
// If logFile is "default", uses GetDefaultLogPath().
// Otherwise uses the specified path.
// When logging to a file, output goes to file only.
func SetupLogger() {
	logFile := viper.GetString(FlagLogFile)

	consoleWriter := zerolog.ConsoleWriter{Out: os.Stderr}

	if logFile == "" {
		// Console only
		log.Logger = log.Output(consoleWriter)
		return
	}

	// Resolve "default" to platform path
	if logFile == DefaultImportLogFile {
		logFile = GetDefaultLogPath()
	}

	// Ensure directory exists
	dir := filepath.Dir(logFile)
	if err := os.MkdirAll(dir, DirPermOwnerRWX); err != nil {
		log.Warn().Err(err).Str("dir", dir).Msg("Failed to create log directory, falling back to console only")
		log.Logger = log.Output(consoleWriter)
		return
	}

	// Create rotating file writer
	fileWriter := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    DefaultLogMaxSize,
		MaxBackups: DefaultLogMaxBackups,
		MaxAge:     DefaultLogMaxAge,
		Compress:   DefaultLogCompress,
	}

	// File only
	log.Logger = log.Output(fileWriter)
}
