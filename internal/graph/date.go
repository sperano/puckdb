package graph

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/pgconv"
)

// parseDateInput parses a GraphQL date argument (pgconv.ParseDate); the
// error names the argument and echoes the value.
func parseDateInput(field, value string) (pgtype.Date, error) {
	parsed, err := pgconv.ParseDate(value)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf(
			"invalid %s %q (expected YYYY-MM-DD): %w",
			field,
			value,
			err,
		)
	}
	return parsed, nil
}

func parseOptionalDateInput(field string, value *string) (pgtype.Date, error) {
	if value == nil {
		return pgtype.Date{}, nil
	}
	return parseDateInput(field, *value)
}
