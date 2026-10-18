package pgconv

import (
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
)

// numericFractionDigits is the number of fractional digits NumericFromFloat
// keeps: enough for costs in USD, temperatures and roto points.
const numericFractionDigits = 6

// NumericFromFloat converts a float64 to a NUMERIC rounded to six
// fractional digits. It goes through the decimal text form so pgtype does
// the big.Int/exponent encoding. NaN becomes a NUMERIC NaN; ±Inf is an
// error.
func NumericFromFloat(f float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(f, 'f', numericFractionDigits, 64)); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

// NumericToFloat converts a NUMERIC to float64. NULL becomes 0 so a
// nullable column cannot turn a sum into NaN.
func NumericToFloat(n pgtype.Numeric) (float64, error) {
	if !n.Valid {
		return 0, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return 0, err
	}
	return f.Float64, nil
}
