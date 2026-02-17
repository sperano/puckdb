package main

import (
	"os"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cmd"
)

func main() {
	if err := cmd.Root().Execute(); err != nil {
		log.Error().Msg(err.Error())
		os.Exit(1)
	}
}
