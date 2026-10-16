package pgconv

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// ParseDate parses a client-supplied date: exactly YYYY-MM-DD (four-digit
// year, two-digit month and day) naming a real calendar day. The error is
// time.Parse's; callers wrap it with their own context (field name, API
// error shape).
//
// pgtype.Date.Scan is not used: it also accepts "infinity" and a " BC"
// suffix and normalizes 2025-02-30 to March 2, so a malformed date would
// silently select another day.
func ParseDate(s string) (pgtype.Date, error) {
	day, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return pgtype.Date{}, err
	}
	return pgtype.Date{Time: day, Valid: true}, nil
}
