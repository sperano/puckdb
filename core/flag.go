package core

import (
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const FlagRedisCacheDuration = "redis_cache_duration"
const FlagMaxGamesImporter = "max_games_importer"
const FlagMaxRostersImporter = "max_rosters_importer"
const FlagMaxTeamSummariesImporter = "max_team_summaries_importer"

func FBool(flags *flag.FlagSet, flag string, value bool, help string) {
	flags.Bool(flag, value, help)
	viper.BindPFlag(flag, flags.Lookup(flag))
}

func FIntP(flags *flag.FlagSet, flag string, short string, value int, help string) {
	flags.IntP(flag, short, value, help)
	viper.BindPFlag(flag, flags.Lookup(flag))
}

func FInt(flags *flag.FlagSet, flag string, value int, help string) {
	flags.Int(flag, value, help)
	viper.BindPFlag(flag, flags.Lookup(flag))
}

func FStringP(flags *flag.FlagSet, flag string, short string, value string, help string) {
	flags.StringP(flag, short, value, help)
	viper.BindPFlag(flag, flags.Lookup(flag))
}

func FString(flags *flag.FlagSet, flag string, value string, help string) {
	flags.String(flag, value, help)
	viper.BindPFlag(flag, flags.Lookup(flag))
}
