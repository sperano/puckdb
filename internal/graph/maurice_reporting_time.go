package graph

import "time"

// The reporting API serves every timestamp in UTC, the zone its day filters
// use. pgx scans timestamptz into the process's local zone, so without these
// the output would follow the server's TZ (UTC in the cluster, not elsewhere).

func reportingTime(t time.Time) time.Time { return t.UTC() }

func reportingTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}
