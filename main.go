// Package main is the puckdb binary entry point; every CLI command (api,
// worker, sync, db, draft, news, sim, yahoo, mcp-server, maurice, metrics,
// redis) is registered under it in cmd.Root.
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
