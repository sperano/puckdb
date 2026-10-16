package shared

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
)

// ParseDate parses a date string using config.DateFormat (YYYY-MM-DD).
// Returns the zero value and logs a warning on parse failure.
func ParseDate(s string) time.Time {
	t, err := time.Parse(config.DateFormat, s)
	if err != nil {
		log.Warn().Err(err).Str("value", s).Msg("failed to parse date")
	}
	return t
}

// ParseDateToPgDate parses a date string into a pgtype.Date.
// Returns an invalid (null) pgtype.Date if the string is empty or unparseable.
func ParseDateToPgDate(s string) pgtype.Date {
	if s == "" {
		return pgtype.Date{}
	}
	t, err := time.Parse(config.DateFormat, s)
	if err != nil {
		log.Warn().Err(err).Str("value", s).Msg("failed to parse date")
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

// ParseTimestamptz parses a timestamp string using the given layout into a pgtype.Timestamptz.
// Returns an invalid (null) pgtype.Timestamptz if the string is empty or unparseable.
func ParseTimestamptz(layout, s string) pgtype.Timestamptz {
	if s == "" {
		return pgtype.Timestamptz{}
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		log.Warn().Err(err).Str("value", s).Msg("failed to parse timestamp")
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}
