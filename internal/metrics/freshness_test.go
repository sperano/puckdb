package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestSetDataLatest_ReplacesDateSeries(t *testing.T) {
	const dataset = "test_set_data_latest"
	t.Cleanup(func() { ClearDataLatest(dataset) })

	first := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	SetDataLatest(dataset, first)
	// 23:30 in Montreal is 04:30 UTC the next day: the label is the UTC date.
	montreal := time.FixedZone("EST", -5*60*60)
	second := time.Date(2026, 11, 13, 23, 30, 0, 0, montreal)
	SetDataLatest(dataset, second)

	assert.Equal(t, float64(second.Unix()), testutil.ToFloat64(dataLatestTimestamp.WithLabelValues(dataset)))
	assert.Equal(t, 1, testutil.CollectAndCount(dataLatestDateInfo), "the earlier date series must be removed")
	assert.Equal(t, float64(1), testutil.ToFloat64(dataLatestDateInfo.WithLabelValues(dataset, "2026-11-14")))
}

func TestClearDataLatest_RemovesSeries(t *testing.T) {
	const dataset = "test_clear_data_latest"
	SetDataLatest(dataset, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))

	ClearDataLatest(dataset)

	assert.Zero(t, testutil.CollectAndCount(dataLatestTimestamp))
	assert.Zero(t, testutil.CollectAndCount(dataLatestDateInfo))
}
