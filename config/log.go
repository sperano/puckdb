package config

import (
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"sort"
	"strings"
)

var logLevels map[string]zerolog.Level

func init() {
	logLevels = make(map[string]zerolog.Level)
	logLevels["debug"] = zerolog.DebugLevel
	logLevels["info"] = zerolog.InfoLevel
	logLevels["warn"] = zerolog.WarnLevel
	logLevels["error"] = zerolog.ErrorLevel
	logLevels["fatal"] = zerolog.FatalLevel
	logLevels["trace"] = zerolog.TraceLevel
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
		log.Fatal().Msgf("Invalid log level: %s", s)
	}
	zerolog.SetGlobalLevel(lvl)
}
