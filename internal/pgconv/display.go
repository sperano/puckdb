package pgconv

import (
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// Display conversions: a NULL column is the empty string.

// FormatDate formats a date as YYYY-MM-DD.
func FormatDate(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format(time.DateOnly)
}

// FormatInt4 formats an int4 in base 10.
func FormatInt4(i pgtype.Int4) string {
	if !i.Valid {
		return ""
	}
	return strconv.FormatInt(int64(i.Int32), 10)
}

// FormatInt8 formats an int8 in base 10.
func FormatInt8(i pgtype.Int8) string {
	if !i.Valid {
		return ""
	}
	return strconv.FormatInt(i.Int64, 10)
}

// FormatPlayerPosition returns the player_position enum value ("C", "D",
// ...).
func FormatPlayerPosition(p sqlcdb.NullPlayerPosition) string {
	if !p.Valid {
		return ""
	}
	return string(p.PlayerPosition)
}
