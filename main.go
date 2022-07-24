package main

import (
	"os"

	"github.com/ericsperano/yfh/cmd"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	viper.AutomaticEnv()
	viper.SetEnvPrefix("yfh")
}

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatal().Err(err)
	}
}
