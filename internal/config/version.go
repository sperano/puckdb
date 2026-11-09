package config

import (
	"runtime"

	"github.com/rs/zerolog/log"
)

var (
	BuildNumber = BuildNumberNotAvailable
)

func LogIntro() {
	log.Info().Int("cpus", runtime.NumCPU()).Str("build", BuildNumber).Msgf("PuckDB")
}
