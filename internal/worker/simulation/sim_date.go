package simulation

import "github.com/jackc/pgx/v5/pgtype"

// addDays returns base + n calendar days: the day loop's simDate
// progression (n = 1) and the waiver processing date. pgtype.Date wraps a
// time.Time; AddDate handles month/year rollover correctly. An invalid
// (NULL) base stays invalid.
func addDays(base pgtype.Date, n int) pgtype.Date {
	if !base.Valid {
		return base
	}
	return pgtype.Date{Time: base.Time.AddDate(0, 0, n), Valid: true}
}
