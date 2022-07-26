package core

import (
	"runtime"

	"github.com/rs/zerolog/log"
)

// TODO publish with a tag with this version too
const Version = "0.6.3"

func LogIntro() {
	log.Info().Int("CPUs", runtime.NumCPU()).Str("Version", Version).Msgf("Yahoo Fantasy Hockey version %s", Version)
}
