package graph

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func parseDateInput(field, value string) (pgtype.Date, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf(
			"invalid %s %q (expected YYYY-MM-DD): %w",
			field,
			value,
			err,
		)
	}
	return pgtype.Date{Time: parsed, Valid: true}, nil
}

func parseOptionalDateInput(field string, value *string) (pgtype.Date, error) {
	if value == nil {
		return pgtype.Date{}, nil
	}
	return parseDateInput(field, *value)
}
