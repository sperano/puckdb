package cmd

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/spf13/viper"
)

// newDraftService builds the ranking service the API and the CLI share, so
// both read the same stored snapshots through the same code.
func newDraftService(pool *pgxpool.Pool) *draftrank.Service {
	return draftrank.NewService(draftrank.NewPGStore(pool), newsadjust.NewRepository(pool), draftrank.ServiceOptions{
		StaleAfter: time.Duration(viper.GetInt(config.FlagDraftStaleAfter)) * time.Hour,
	})
}
