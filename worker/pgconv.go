package worker

import (
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// integer covers all Go integer types and named types derived from them.
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Pointer-to-pgtype conversion helpers.
// These eliminate repetitive nil-check boilerplate when converting
// nhl-api-go optional fields (*T) into pgx nullable column types.

func ptrToInt2[T integer](p *T) pgtype.Int2 {
	if p == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*p), Valid: true}
}

func ptrToInt4[T integer](p *T) pgtype.Int4 {
	if p == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*p), Valid: true}
}

func ptrToInt8[T integer](p *T) pgtype.Int8 {
	if p == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: int64(*p), Valid: true}
}

func ptrToText[T ~string](p *T) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(*p), Valid: true}
}

func ptrToTextTrimmed(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: strings.TrimSpace(*p), Valid: true}
}
