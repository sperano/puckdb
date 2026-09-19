package main

import (
	"context"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cmd"
)

func main() {
	ctx, stop := cmd.SignalContext(context.Background())
	defer stop()
	if err := cmd.Root().ExecuteContext(ctx); err != nil {
		log.Error().Msg(err.Error())
		os.Exit(1)
	}
}
