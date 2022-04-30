package core

import (
	"fmt"
	"sort"

	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const FlagLogLevel = "log_level"

var LogLevels map[string]log.Level

func init() {
	LogLevels = make(map[string]log.Level)
	LogLevels["debug"] = log.DebugLevel
	LogLevels["info"] = log.InfoLevel
	LogLevels["warn"] = log.WarnLevel
	LogLevels["error"] = log.ErrorLevel
	LogLevels["fatal"] = log.FatalLevel
	LogLevels["trace"] = log.TraceLevel
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
		log.Fatalf("Invalid log level: %s", s)
	}
	log.SetLevel(lvl)
}
