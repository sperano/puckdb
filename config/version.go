package config

import (
	"runtime"
	"strconv"

	"github.com/rs/zerolog/log"
)

var (
	BuildNumber = "n/a"
)

func LogIntro() {
	log.Info().Int("CPUs", runtime.NumCPU()).Str("build", BuildNumber).Msgf("PuckDB")
}

// GetBuildNumberAsInt returns the build number as an integer.
// Returns -1 if the build number cannot be parsed.
func GetBuildNumberAsInt() int {
	n, err := strconv.Atoi(BuildNumber)
	if err != nil {
		log.Warn().Str("build_number", BuildNumber).Msg("Could not parse build number as int")
		return -1
	}
	return n
}
