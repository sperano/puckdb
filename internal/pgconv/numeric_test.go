package pgconv

import (
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNumericFromFloatRoundTrip(t *testing.T) {
	t.Parallel()

	for _, want := range []float64{0, 0.7, 200, 12.345678, -12.345678, 0.000001, 1e15} {
		n, err := NumericFromFloat(want)
		require.NoError(t, err)
		require.True(t, n.Valid)
		got, err := NumericToFloat(n)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

// TestNumericFromFloatRoundsToSixDigits pins the stored text of values
// past the sixth fractional digit.
func TestNumericFromFloatRoundsToSixDigits(t *testing.T) {
	t.Parallel()

	cases := map[float64]string{
		12.3456789:  "12.345679",
		-12.3456789: "-12.345679",
		0.0000004:   "0.000000",
		0.0000015:   "0.000002",
		-0.0000004:  "0.000000",
		1e20:        "100000000000000000000.000000",
	}
	for in, want := range cases {
		n, err := NumericFromFloat(in)
		require.NoError(t, err)
		assert.Equal(t, want, numericText(t, n), "input %g", in)
	}
}

func TestNumericFromFloatNonFinite(t *testing.T) {
	t.Parallel()

	nan, err := NumericFromFloat(math.NaN())
	require.NoError(t, err)
	assert.True(t, nan.Valid)
	assert.True(t, nan.NaN)

	for _, inf := range []float64{math.Inf(1), math.Inf(-1)} {
		n, err := NumericFromFloat(inf)
		assert.Error(t, err, "%g", inf)
		assert.False(t, n.Valid)
	}
}

func TestNumericToFloat(t *testing.T) {
	t.Parallel()

	got, err := NumericToFloat(pgtype.Numeric{})
	require.NoError(t, err)
	assert.Zero(t, got, "NULL is 0")

	var n pgtype.Numeric
	require.NoError(t, n.Scan("-3.5"))
	got, err = NumericToFloat(n)
	require.NoError(t, err)
	assert.Equal(t, -3.5, got)

	require.NoError(t, n.Scan("NaN"))
	got, err = NumericToFloat(n)
	require.NoError(t, err)
	assert.True(t, math.IsNaN(got))
}

func numericText(t *testing.T, n pgtype.Numeric) string {
	t.Helper()
	v, err := n.Value()
	require.NoError(t, err)
	s, ok := v.(string)
	require.True(t, ok)
	return s
}
