package core

import (
	"runtime"

	"github.com/dustin/go-humanize/english"
	"github.com/rs/zerolog/log"
)

// TODO publish with a tag with this version too
const Version = "0.6.0-dev"

func LogIntro() {
	log.Info().Str("Version", Version).Msgf("Yahoo Fantasy Hockey version %s", Version)
	log.Info().Int("CPUs", runtime.NumCPU()).Msg(english.Plural(runtime.NumCPU(), "CPU", ""))
}
