// Package pgconv holds the pgtype conversions shared by the GraphQL
// resolvers, the workers, the simulation and the MCP server: optional Go
// values to nullable columns, nullable columns to display strings, strict
// YYYY-MM-DD dates and float64/NUMERIC round trips.
//
// Only conversions whose semantics are identical at every call site live
// here. Mappings onto a caller's API shape (GraphQL pointers, MCP float
// formatting, tool error text) stay with that caller.
package pgconv

import (
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// Integer covers all Go integer types and named types derived from them.
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Pointer-to-pgtype conversions: nil becomes SQL NULL, anything else a
// valid value. The integer helpers narrow like a Go conversion; callers
// bound their inputs first.

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

func PtrToBool(p *bool) pgtype.Bool {
	if p == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *p, Valid: true}
}
