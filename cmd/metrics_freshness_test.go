package cmd

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freshnessTables are the tables the freshness datasets read.
var freshnessTables = []string{
	"games", "nhl_day_imports", "standings_snapshots", "yahoo_team_rosters",
	"yahoo_team_summaries", "yahoo_transactions", "news_articles", "news_events",
	"draft_ranking_snapshots", "maurice_turns",
}

const freshnessMetricTimestamp = "puckdb_data_latest_timestamp"
const freshnessMetricDateInfo = "puckdb_data_latest_date_info"

// TestCollectFreshnessMetrics_EmptyDatabaseClearsEveryDataset proves every
// dataset query runs on the migrated schema: a failed query would keep the
// value set before the collection.
func TestCollectFreshnessMetrics_EmptyDatabaseClearsEveryDataset(t *testing.T) {
	pool := pgtest.Open(t, freshnessTables...)
	for _, ds := range freshnessDatasets {
		metrics.SetDataLatest(ds.name, time.Unix(0, 0))
	}

	collectFreshnessMetrics(context.Background(), pool)

	count, err := testutil.GatherAndCount(metrics.CollectorRegistry, freshnessMetricTimestamp, freshnessMetricDateInfo)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestCollectFreshnessMetrics_ReportsNewestData(t *testing.T) {
	pool := pgtest.Open(t, freshnessTables...)
	ctx := context.Background()
	t.Cleanup(func() {
		for _, ds := range freshnessDatasets {
			metrics.ClearDataLatest(ds.name)
		}
	})

	const playerID = 8_990_001
	_, err := pool.Exec(ctx, `
		INSERT INTO players (id, first_name, last_name) VALUES ($1, 'Test', 'Freshness')
		ON CONFLICT (id) DO NOTHING`, playerID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id) VALUES
			(2026020101, 20262027, 'regular_season', '2026-11-11', 'OFF', 1, 2),
			(2026020102, 20262027, 'regular_season', '2026-11-12', 'FINAL', 1, 2),
			(2026020103, 20262027, 'regular_season', '2026-11-13', 'LIVE', 1, 2),
			(2026020104, 20262027, 'regular_season', '2026-11-20', 'FUT', 1, 2)`)
	require.NoError(t, err)
	// Box scores only for the first game: nhl_boxscores lags nhl_games_final.
	_, err = pool.Exec(ctx, `
		INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, "position")
		VALUES (2026020101, $1, 1, true, 9, 'C')`, playerID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO news_articles (publisher, external_id, source_id, kind, url, first_seen_at, last_seen_at)
		VALUES ('nhl', 'freshness-1', 'nhl', 'official', 'https://example.test/1',
			'2026-11-10T08:00:00Z', '2026-11-13T23:30:00-05:00')`)
	require.NoError(t, err)

	collectFreshnessMetrics(ctx, pool)

	expected := `
# HELP puckdb_data_latest_date_info Always 1; the date label is the UTC date (YYYY-MM-DD) of the newest data in a dataset
# TYPE puckdb_data_latest_date_info gauge
puckdb_data_latest_date_info{dataset="news_articles",date="2026-11-14"} 1
puckdb_data_latest_date_info{dataset="nhl_boxscores",date="2026-11-11"} 1
puckdb_data_latest_date_info{dataset="nhl_games_final",date="2026-11-12"} 1
# HELP puckdb_data_latest_timestamp Unix timestamp of the newest data in a dataset (a date column counts as 00:00 UTC of that date)
# TYPE puckdb_data_latest_timestamp gauge
puckdb_data_latest_timestamp{dataset="news_articles"} ` + unixText(time.Date(2026, 11, 14, 4, 30, 0, 0, time.UTC)) + `
puckdb_data_latest_timestamp{dataset="nhl_boxscores"} ` + unixText(time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)) + `
puckdb_data_latest_timestamp{dataset="nhl_games_final"} ` + unixText(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)) + `
`
	require.NoError(t, testutil.GatherAndCompare(metrics.CollectorRegistry, strings.NewReader(expected),
		freshnessMetricTimestamp, freshnessMetricDateInfo))
}

// unixText formats a timestamp as Unix seconds for the expected exposition.
func unixText(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}
