package graph

import "time"

// reportingTime returns t in UTC, the zone the reporting API serves every
// timestamp in and its day filters use. pgx scans timestamptz into the
// process's local zone, so without this the output would follow the server's
// TZ (UTC in the cluster, not elsewhere).
func reportingTime(t time.Time) time.Time { return t.UTC() }

// reportingTimePtr is reportingTime for an optional timestamp; nil stays nil
// and the input is not modified.
func reportingTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}
