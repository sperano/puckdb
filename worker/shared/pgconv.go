package shared

import (
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
)

// Integer covers all Go integer types and named types derived from them.
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Pointer-to-pgtype conversion helpers.
// These eliminate repetitive nil-check boilerplate when converting
// nhl-api-go optional fields (*T) into pgx nullable column types.

func PtrToInt2[T Integer](p *T) pgtype.Int2 {
	if p == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*p), Valid: true}
}

func PtrToInt4[T Integer](p *T) pgtype.Int4 {
	if p == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*p), Valid: true}
}

func PtrToInt8[T Integer](p *T) pgtype.Int8 {
	if p == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: int64(*p), Valid: true}
}

func PtrToText[T ~string](p *T) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(*p), Valid: true}
}

func PtrToTextTrimmed(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: strings.TrimSpace(*p), Valid: true}
}

// ParseDate parses a date string using config.DateFormat ("2006-01-02").
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
