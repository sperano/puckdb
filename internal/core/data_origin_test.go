package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOriginCounts_Record(t *testing.T) {
	t.Parallel()
	c := OriginCounts{}
	c.Record(OriginRedis)
	c.Record(OriginRedis)
	c.Record(OriginRemoteNHLAPI)

	assert.Equal(t, 2, c[OriginRedis])
	assert.Equal(t, 1, c[OriginRemoteNHLAPI])
}

func TestOriginCounts_Add(t *testing.T) {
	t.Parallel()
	a := OriginCounts{OriginRedis: 3, OriginRemoteNHLAPI: 1}
	b := OriginCounts{OriginRedis: 2, OriginFileSystem: 5}

	a.Add(b)

	assert.Equal(t, 5, a[OriginRedis])
	assert.Equal(t, 1, a[OriginRemoteNHLAPI])
	assert.Equal(t, 5, a[OriginFileSystem])
}

func TestOriginCounts_AddNil(t *testing.T) {
	t.Parallel()
	c := OriginCounts{OriginRedis: 1}
	c.Add(nil)

	assert.Equal(t, 1, c[OriginRedis])
}

func TestOriginCounts_Total(t *testing.T) {
	t.Parallel()
	c := OriginCounts{OriginRedis: 3, OriginRemoteNHLAPI: 2, OriginFileSystem: 5}
	assert.Equal(t, 10, c.Total())
}

func TestOriginCounts_TotalEmpty(t *testing.T) {
	t.Parallel()
	var c OriginCounts
	assert.Equal(t, 0, c.Total())
}

func TestDataOrigin_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		origin DataOrigin
		want   string
	}{
		{"valid origin", OriginRedis, "Redis"},
		{"unknown", OriginUnknown, "Unknown"},
		{"negative", DataOrigin(-1), "Unknown"},
		{"out-of-range", DataOrigin(len(originNames)), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.origin.String())
		})
	}
}

func TestOriginCounts_AppendSummary(t *testing.T) {
	t.Parallel()

	t.Run("non-empty counts appends summary", func(t *testing.T) {
		t.Parallel()
		c := OriginCounts{OriginRedis: 4}
		result := c.AppendSummary("fetched 4 schedules", "origins")
		assert.Equal(t, "fetched 4 schedules (origins: 100% redis)", result)
	})

	t.Run("empty counts returns original message", func(t *testing.T) {
		t.Parallel()
		c := OriginCounts{}
		result := c.AppendSummary("fetched 0 schedules", "origins")
		assert.Equal(t, "fetched 0 schedules", result)
	})
}

func TestOriginCounts_Summary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		counts   OriginCounts
		expected string
	}{
		{
			name:     "single origin",
			counts:   OriginCounts{OriginRedis: 4},
			expected: "(test: 100% redis)",
		},
		{
			name:     "two origins sorted by count desc",
			counts:   OriginCounts{OriginRedis: 3, OriginRemoteNHLAPI: 1},
			expected: "(test: 75% redis, 25% remotenhlapi)",
		},
		{
			name:     "three origins",
			counts:   OriginCounts{OriginRedis: 5, OriginFileSystem: 3, OriginRemoteNHLAPI: 2},
			expected: "(test: 50% redis, 30% filesystem, 20% remotenhlapi)",
		},
		{
			name:     "rounding sums to 100",
			counts:   OriginCounts{OriginRedis: 2, OriginFileSystem: 1},
			expected: "(test: 67% redis, 33% filesystem)",
		},
		{
			name:     "rounding across three origins",
			counts:   OriginCounts{OriginRedis: 5, OriginFileSystem: 3, OriginRemoteNHLAPI: 1},
			expected: "(test: 56% redis, 33% filesystem, 11% remotenhlapi)",
		},
		{
			name:     "equal counts tie-broken by origin",
			counts:   OriginCounts{OriginFileSystem: 1, OriginRedis: 1},
			expected: "(test: 50% filesystem, 50% redis)",
		},
		{
			name:     "empty",
			counts:   OriginCounts{},
			expected: "",
		},
		{
			name:     "nil",
			counts:   nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.counts.Summary("test"))
		})
	}
}
