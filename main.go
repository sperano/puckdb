package main

import (
	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/cmd"
)

func main() {
	if err := cmd.Root().Execute(); err != nil {
		log.Error().Msg(err.Error())
	}
}
