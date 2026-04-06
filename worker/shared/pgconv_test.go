package shared

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPtrToInt2(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer returns invalid Int2", func(t *testing.T) {
		t.Parallel()
		result := PtrToInt2[int](nil)
		assert.False(t, result.Valid)
		assert.Equal(t, int16(0), result.Int16)
	})

	t.Run("non-nil pointer returns valid Int2 with correct value", func(t *testing.T) {
		t.Parallel()
		v := 42
		result := PtrToInt2(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int16(42), result.Int16)
	})

	t.Run("zero value returns valid Int2", func(t *testing.T) {
		t.Parallel()
		v := 0
		result := PtrToInt2(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int16(0), result.Int16)
	})

	t.Run("negative value is preserved", func(t *testing.T) {
		t.Parallel()
		v := -7
		result := PtrToInt2(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int16(-7), result.Int16)
	})
}

func TestPtrToInt4(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer returns invalid Int4", func(t *testing.T) {
		t.Parallel()
		result := PtrToInt4[int](nil)
		assert.False(t, result.Valid)
		assert.Equal(t, int32(0), result.Int32)
	})

	t.Run("non-nil pointer returns valid Int4 with correct value", func(t *testing.T) {
		t.Parallel()
		v := 100000
		result := PtrToInt4(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int32(100000), result.Int32)
	})

	t.Run("zero value returns valid Int4", func(t *testing.T) {
		t.Parallel()
		v := 0
		result := PtrToInt4(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int32(0), result.Int32)
	})
}

func TestPtrToInt8(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer returns invalid Int8", func(t *testing.T) {
		t.Parallel()
		result := PtrToInt8[int](nil)
		assert.False(t, result.Valid)
		assert.Equal(t, int64(0), result.Int64)
	})

	t.Run("non-nil pointer returns valid Int8 with correct value", func(t *testing.T) {
		t.Parallel()
		v := int64(9999999999)
		result := PtrToInt8(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int64(9999999999), result.Int64)
	})

	t.Run("zero value returns valid Int8", func(t *testing.T) {
		t.Parallel()
		v := int64(0)
		result := PtrToInt8(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int64(0), result.Int64)
	})
}

func TestPtrToText(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer returns invalid Text", func(t *testing.T) {
		t.Parallel()
		result := PtrToText[string](nil)
		assert.False(t, result.Valid)
		assert.Equal(t, "", result.String)
	})

	t.Run("non-nil pointer returns valid Text with string value", func(t *testing.T) {
		t.Parallel()
		v := "hello"
		result := PtrToText(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, "hello", result.String)
	})

	t.Run("empty string pointer returns valid Text", func(t *testing.T) {
		t.Parallel()
		v := ""
		result := PtrToText(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, "", result.String)
	})
}

func TestPtrToTextTrimmed(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer returns invalid Text", func(t *testing.T) {
		t.Parallel()
		result := PtrToTextTrimmed(nil)
		assert.False(t, result.Valid)
	})

	t.Run("string with leading and trailing spaces is trimmed", func(t *testing.T) {
		t.Parallel()
		v := "  hello world  "
		result := PtrToTextTrimmed(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, "hello world", result.String)
	})

	t.Run("string with no spaces is returned as-is", func(t *testing.T) {
		t.Parallel()
		v := "nospaces"
		result := PtrToTextTrimmed(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, "nospaces", result.String)
	})

	t.Run("whitespace-only string trims to empty", func(t *testing.T) {
		t.Parallel()
		v := "   "
		result := PtrToTextTrimmed(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, "", result.String)
	})
}

func TestParseDate(t *testing.T) {
	t.Parallel()

	t.Run("valid date string returns correct time", func(t *testing.T) {
		t.Parallel()
		result := ParseDate("2023-10-10")
		require.False(t, result.IsZero())
		assert.Equal(t, 2023, result.Year())
		assert.Equal(t, time.October, result.Month())
		assert.Equal(t, 10, result.Day())
	})

	t.Run("invalid date string returns zero value", func(t *testing.T) {
		t.Parallel()
		result := ParseDate("not-a-date")
		assert.True(t, result.IsZero())
	})

	t.Run("empty string returns zero value", func(t *testing.T) {
		t.Parallel()
		result := ParseDate("")
		assert.True(t, result.IsZero())
	})
}

func TestParseDateToPgDate(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns invalid Date", func(t *testing.T) {
		t.Parallel()
		result := ParseDateToPgDate("")
		assert.False(t, result.Valid)
	})

	t.Run("valid date string returns correct pgtype.Date", func(t *testing.T) {
		t.Parallel()
		result := ParseDateToPgDate("2024-01-15")
		require.True(t, result.Valid)
		assert.Equal(t, 2024, result.Time.Year())
		assert.Equal(t, time.January, result.Time.Month())
		assert.Equal(t, 15, result.Time.Day())
	})

	t.Run("invalid date string returns invalid Date", func(t *testing.T) {
		t.Parallel()
		result := ParseDateToPgDate("15/01/2024")
		assert.False(t, result.Valid)
	})
}

func TestParseTimestamptz(t *testing.T) {
	t.Parallel()

	const rfc3339Layout = "2006-01-02T15:04:05Z07:00"

	t.Run("empty string returns invalid Timestamptz", func(t *testing.T) {
		t.Parallel()
		result := ParseTimestamptz(rfc3339Layout, "")
		assert.False(t, result.Valid)
	})

	t.Run("valid timestamp returns correct Timestamptz", func(t *testing.T) {
		t.Parallel()
		result := ParseTimestamptz(rfc3339Layout, "2024-03-20T14:30:00Z")
		require.True(t, result.Valid)
		assert.Equal(t, 2024, result.Time.Year())
		assert.Equal(t, time.March, result.Time.Month())
		assert.Equal(t, 20, result.Time.Day())
		assert.Equal(t, 14, result.Time.Hour())
		assert.Equal(t, 30, result.Time.Minute())
	})

	t.Run("invalid timestamp returns invalid Timestamptz", func(t *testing.T) {
		t.Parallel()
		result := ParseTimestamptz(rfc3339Layout, "not-a-timestamp")
		assert.False(t, result.Valid)
	})
}
