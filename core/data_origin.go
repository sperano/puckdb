package core

import (
	"fmt"
	"sort"
	"strings"
)

// RedisResourceKeyPrefix is the prefix used for all resource Redis keys.
const RedisResourceKeyPrefix = "puckdb:resource:"

// DataOrigin identifies where resource data was retrieved from.
type DataOrigin int

const (
	OriginUnknown DataOrigin = iota
	OriginRemoteNHLAPI
	OriginRemoteYahooAPI
	OriginFileSystem
	OriginMemFileSystem
	OriginRedis
)

var originNames = [...]string{
	OriginUnknown:        "Unknown",
	OriginRemoteNHLAPI:   "RemoteNHLAPI",
	OriginRemoteYahooAPI: "RemoteYahooAPI",
	OriginFileSystem:     "FileSystem",
	OriginMemFileSystem:  "MemFileSystem",
	OriginRedis:          "Redis",
}

// String returns the string representation of the DataOrigin.
func (o DataOrigin) String() string {
	if o < 0 || int(o) >= len(originNames) {
		return originNames[OriginUnknown]
	}
	return originNames[o]
}

// OriginCounts tracks fetch operations by data source.
type OriginCounts map[DataOrigin]int

// Record increments the counter for the given origin.
func (c OriginCounts) Record(origin DataOrigin) {
	c[origin]++
}

// Add accumulates all counts from another OriginCounts.
func (c OriginCounts) Add(other OriginCounts) {
	for origin, count := range other {
		c[origin] += count
	}
}

// Total returns the sum of all counts.
func (c OriginCounts) Total() int {
	var total int
	for _, count := range c {
		total += count
	}
	return total
}

// AppendSummary appends the origin summary to msg if counts are non-empty.
func (c OriginCounts) AppendSummary(msg, label string) string {
	if s := c.Summary(label); s != "" {
		return msg + " " + s
	}
	return msg
}

// Summary returns a per-origin percentage breakdown like "(schedules origins: 75% redis, 25% filesystem)"
// or "" if empty. Origins are sorted by count descending.
func (c OriginCounts) Summary(label string) string {
	total := c.Total()
	if total == 0 {
		return ""
	}

	type entry struct {
		origin DataOrigin
		count  int
	}
	entries := make([]entry, 0, len(c))
	for origin, count := range c {
		if count > 0 {
			entries = append(entries, entry{origin, count})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].origin < entries[j].origin
	})

	// Largest-remainder method: assign floor percentages, then distribute
	// the remaining points to entries with the largest fractional remainders
	// so the total always sums to exactly 100.
	pcts := make([]int, len(entries))
	remainders := make([]int, len(entries))
	floorSum := 0
	for i, e := range entries {
		pcts[i] = e.count * 100 / total
		remainders[i] = e.count * 100 % total
		floorSum += pcts[i]
	}
	deficit := 100 - floorSum
	// Build index list sorted by remainder descending (stable by original order).
	idx := make([]int, len(entries))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return remainders[idx[a]] > remainders[idx[b]]
	})
	for i := 0; i < deficit; i++ {
		pcts[idx[i]]++
	}

	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = fmt.Sprintf("%d%% %s", pcts[i], strings.ToLower(e.origin.String()))
	}
	return "(" + label + ": " + strings.Join(parts, ", ") + ")"
}
