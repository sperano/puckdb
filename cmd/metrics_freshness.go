package cmd

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/metrics"
)

// freshnessDataset is one dataset of puckdb_data_latest_timestamp: its query
// returns one nullable timestamptz, NULL when the dataset holds no data. A
// date column is read as 00:00 UTC of that date (AT TIME ZONE 'UTC'), so the
// metric's UTC date label is the stored date.
type freshnessDataset struct {
	name  string
	query string
}

// latestGameWithRows is the date of the newest game that has rows in the
// given per-game table, walking games newest first (idx_games_game_date) and
// probing the table's game_id-leading index.
func latestGameWithRows(table string) string {
	return `SELECT (SELECT g.game_date::timestamp AT TIME ZONE 'UTC' FROM games g
		WHERE EXISTS (SELECT 1 FROM ` + table + ` t WHERE t.game_id = g.id)
		ORDER BY g.game_date DESC LIMIT 1)`
}

// freshnessDatasets lists the datasets the database collector reports. Each
// name is the metric's dataset label: keep it stable, dashboards select on it.
var freshnessDatasets = []freshnessDataset{
	{"nhl_games_final", `SELECT max(game_date)::timestamp AT TIME ZONE 'UTC' FROM games
		WHERE game_state IN ('OFF', 'FINAL')`},
	{"nhl_boxscores", latestGameWithRows("game_skater_stats")},
	{"nhl_play_by_play", latestGameWithRows("play_events")},
	{"nhl_shifts", latestGameWithRows("shifts")},
	{"nhl_day_imports", `SELECT max(date)::timestamp AT TIME ZONE 'UTC' FROM nhl_day_imports`},
	{"nhl_standings", `SELECT max(date)::timestamp AT TIME ZONE 'UTC' FROM standings_snapshots`},
	{"yahoo_rosters", `SELECT max(date)::timestamp AT TIME ZONE 'UTC' FROM yahoo_team_rosters`},
	{"yahoo_team_summaries", `SELECT max(date)::timestamp AT TIME ZONE 'UTC' FROM yahoo_team_summaries`},
	{"yahoo_standings", `SELECT max(date)::timestamp AT TIME ZONE 'UTC' FROM yahoo_team_summaries
		WHERE standings_captured_at IS NOT NULL`},
	{"yahoo_transactions", `SELECT to_timestamp(max("timestamp")) FROM yahoo_transactions`},
	{"news_articles", `SELECT max(last_seen_at) FROM news_articles`},
	{"news_events", `SELECT max(last_reported_at) FROM news_events`},
	{"draft_rankings", `SELECT max(as_of) FROM draft_ranking_snapshots`},
	{"maurice_turns", `SELECT max(started_at) FROM maurice_turns`},
}

// collectFreshnessMetrics publishes the newest data of every dataset. A
// failed query is logged and keeps the dataset's previous value; an empty
// dataset has its series removed.
func collectFreshnessMetrics(ctx context.Context, pool *pgxpool.Pool) {
	for _, ds := range freshnessDatasets {
		var latest *time.Time
		if err := pool.QueryRow(ctx, ds.query).Scan(&latest); err != nil {
			log.Warn().Err(err).Str("dataset", ds.name).Msg("Failed to query dataset freshness")
			continue
		}
		if latest == nil {
			metrics.ClearDataLatest(ds.name)
			continue
		}
		metrics.SetDataLatest(ds.name, *latest)
	}
}
