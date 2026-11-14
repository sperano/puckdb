package cmd

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freshnessTables are the tables the freshness datasets read, and the parent
// tables their fixtures insert into.
var freshnessTables = []string{
	"games", "game_skater_stats", "play_events", "shifts", "nhl_day_imports", "standings_snapshots",
	"yahoo_leagues", "yahoo_team_rosters", "yahoo_team_summaries", "yahoo_transactions",
	"news_articles", "news_events", "projection_snapshots", "draft_ranking_snapshots",
	"app_users", "maurice_turns",
}

const freshnessMetricTimestamp = "puckdb_data_latest_timestamp"
const freshnessMetricDateInfo = "puckdb_data_latest_date_info"

// freshnessDateLayout is the date label layout of puckdb_data_latest_date_info.
const freshnessDateLayout = "2006-01-02"

// openFreshnessPool opens the test database with the freshness tables
// emptied and clears every dataset's series once the test ends.
func openFreshnessPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t, freshnessTables...)
	t.Cleanup(clearFreshnessMetrics)
	return pool
}

func clearFreshnessMetrics() {
	for _, ds := range freshnessDatasets {
		metrics.ClearDataLatest(ds.name)
	}
}

// requireFreshness checks that the freshness series are exactly want: one
// timestamp and one date series per dataset, none for the others.
func requireFreshness(t *testing.T, want map[string]time.Time) {
	t.Helper()
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# HELP puckdb_data_latest_date_info Always 1; the date label is the UTC date (YYYY-MM-DD) of the newest data in a dataset\n")
	b.WriteString("# TYPE puckdb_data_latest_date_info gauge\n")
	for _, name := range names {
		b.WriteString(`puckdb_data_latest_date_info{dataset="` + name + `",date="` +
			want[name].UTC().Format(freshnessDateLayout) + "\"} 1\n")
	}
	b.WriteString("# HELP puckdb_data_latest_timestamp Unix timestamp of the newest data in a dataset (a date column counts as 00:00 UTC of that date)\n")
	b.WriteString("# TYPE puckdb_data_latest_timestamp gauge\n")
	for _, name := range names {
		b.WriteString(`puckdb_data_latest_timestamp{dataset="` + name + `"} ` +
			strconv.FormatInt(want[name].Unix(), 10) + "\n")
	}
	require.NoError(t, testutil.GatherAndCompare(metrics.CollectorRegistry, strings.NewReader(b.String()),
		freshnessMetricTimestamp, freshnessMetricDateInfo))
}

// freshnessSessionZones are the session time zones every dataset is
// collected in: a date column must read as 00:00 UTC whatever the session's
// zone, so a query that drops AT TIME ZONE 'UTC' fails in the second one.
var freshnessSessionZones = []string{"UTC", "America/Montreal"}

// requireFreshnessInZones collects on a pool per session time zone, each
// time from cleared series, and checks the series are exactly want.
func requireFreshnessInZones(t *testing.T, pool *pgxpool.Pool, want map[string]time.Time) {
	t.Helper()
	for _, zone := range freshnessSessionZones {
		config := pool.Config()
		config.ConnConfig.RuntimeParams["timezone"] = zone
		zonePool, err := pgxpool.NewWithConfig(context.Background(), config)
		require.NoError(t, err)
		clearFreshnessMetrics()
		collectFreshnessMetrics(context.Background(), zonePool)
		zonePool.Close()
		t.Run(zone, func(t *testing.T) { requireFreshness(t, want) })
	}
}

// utcDay is 00:00 UTC of a November 2026 day, the value of a date column.
func utcDay(day int) time.Time {
	return time.Date(2026, time.November, day, 0, 0, 0, 0, time.UTC)
}

// TestCollectFreshnessMetrics_EmptyDatabaseClearsEveryDataset proves every
// dataset query runs on the migrated schema: a failed query would keep the
// value set before the collection
// (TestCollectFreshnessMetrics_FailedQueryKeepsLastValue).
func TestCollectFreshnessMetrics_EmptyDatabaseClearsEveryDataset(t *testing.T) {
	pool := openFreshnessPool(t)
	for _, ds := range freshnessDatasets {
		metrics.SetDataLatest(ds.name, time.Unix(0, 0))
	}

	collectFreshnessMetrics(context.Background(), pool)

	count, err := testutil.GatherAndCount(metrics.CollectorRegistry, freshnessMetricTimestamp, freshnessMetricDateInfo)
	require.NoError(t, err)
	assert.Zero(t, count)
}

// TestCollectFreshnessMetrics_FailedQueryKeepsLastValue runs every query on
// a cancelled context, so each one fails before reaching a server.
func TestCollectFreshnessMetrics_FailedQueryKeepsLastValue(t *testing.T) {
	t.Cleanup(clearFreshnessMetrics)
	config, err := pgxpool.ParseConfig("postgres://freshness@127.0.0.1:1/unreachable_test")
	require.NoError(t, err)
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	want := make(map[string]time.Time, len(freshnessDatasets))
	for i, ds := range freshnessDatasets {
		want[ds.name] = utcDay(1).Add(time.Duration(i) * time.Hour)
		metrics.SetDataLatest(ds.name, want[ds.name])
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	collectFreshnessMetrics(ctx, pool)

	requireFreshness(t, want)
}

// TestCollectFreshnessMetrics_NHLDatasets: every NHL dataset expects another
// date, so a dataset pointed at another's query or table fails.
func TestCollectFreshnessMetrics_NHLDatasets(t *testing.T) {
	pool := openFreshnessPool(t)
	seedFreshnessGames(t, pool)
	seedFreshnessNHLDays(t, pool)

	requireFreshnessInZones(t, pool, map[string]time.Time{
		"nhl_games_final":  utcDay(12), // the OFF game; LIVE and FUT games are newer
		"nhl_boxscores":    utcDay(10),
		"nhl_shifts":       utcDay(13),
		"nhl_play_by_play": utcDay(8),
		"nhl_day_imports":  utcDay(11), // by date, not by the later imported_at
		"nhl_standings":    utcDay(14),
	})
}

func TestCollectFreshnessMetrics_YahooDatasets(t *testing.T) {
	pool := openFreshnessPool(t)
	seedFreshnessYahoo(t, pool)

	requireFreshnessInZones(t, pool, map[string]time.Time{
		"yahoo_rosters":        utcDay(12),
		"yahoo_team_summaries": utcDay(13),
		"yahoo_standings":      utcDay(10), // the newest summary has no standings snapshot
		"yahoo_transactions":   freshnessTransactionTime,
	})
}

func TestCollectFreshnessMetrics_NewsDraftAndMauriceDatasets(t *testing.T) {
	pool := openFreshnessPool(t)
	seedFreshnessNews(t, pool)
	seedFreshnessDraftRankings(t, pool)
	seedFreshnessMauriceTurns(t, pool)

	requireFreshnessInZones(t, pool, map[string]time.Time{
		// 2026-11-13T23:30-05:00 is 2026-11-14 in UTC, the date label's day.
		"news_articles":  time.Date(2026, time.November, 14, 4, 30, 0, 0, time.UTC),
		"news_events":    time.Date(2026, time.November, 12, 10, 0, 0, 0, time.UTC),
		"draft_rankings": time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC),
		"maurice_turns":  time.Date(2026, time.November, 13, 20, 0, 0, 0, time.UTC),
	})
}
