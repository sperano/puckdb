package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFetchStatsAdd(t *testing.T) {
	total := FetchStats{Downloaded: 3, CacheHits: 5, Missing: 1}
	batch := FetchStats{Downloaded: 2, CacheHits: 4, Missing: 3}

	total.Add(batch)

	assert.Equal(t, 5, total.Downloaded)
	assert.Equal(t, 9, total.CacheHits)
	assert.Equal(t, 4, total.Missing)
}

func TestFetchStatsAddZero(t *testing.T) {
	total := FetchStats{Downloaded: 1, CacheHits: 2, Missing: 3}
	total.Add(FetchStats{})

	assert.Equal(t, 1, total.Downloaded)
	assert.Equal(t, 2, total.CacheHits)
	assert.Equal(t, 3, total.Missing)
}
