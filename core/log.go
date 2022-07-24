package core

import (
	"fmt"
	"sort"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const FlagLogLevel = "log_level"

var LogLevels map[string]zerolog.Level

func init() {
	LogLevels = make(map[string]zerolog.Level)
	LogLevels["debug"] = zerolog.DebugLevel
	LogLevels["info"] = zerolog.InfoLevel
	LogLevels["warn"] = zerolog.WarnLevel
	LogLevels["error"] = zerolog.ErrorLevel
	LogLevels["fatal"] = zerolog.FatalLevel
	//LogLevels["trace"] = zerolog.TraceLevel
}

func SetupViperLogLevel(flags *flag.FlagSet) {
	arr := []string{}
	for k := range LogLevels {
		arr = append(arr, k)
	}
	sort.Strings(arr)
	FStringP(flags, FlagLogLevel, "L", "info", fmt.Sprintf("Log Level: %s", arr))
}

func SetLogLevel() {
	s := viper.GetString(FlagLogLevel)
	lvl, found := LogLevels[s]
	if !found {
		log.Fatal().Msgf("Invalid log level: %s", s)
	}
	zerolog.SetGlobalLevel(lvl)
}
