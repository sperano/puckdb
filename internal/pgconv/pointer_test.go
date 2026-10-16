package pgconv

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestPtrToBool(t *testing.T) {
	t.Parallel()

	assert.False(t, PtrToBool(nil).Valid)
	for _, v := range []bool{true, false} {
		got := PtrToBool(&v)
		assert.True(t, got.Valid)
		assert.Equal(t, v, got.Bool)
	}
}

// TestPtrToIntBoundaries pins the column-type limits and the Go-conversion
// narrowing past them, which callers rely on bounding their inputs first.
func TestPtrToIntBoundaries(t *testing.T) {
	t.Parallel()

	minInt16, maxInt16 := math.MinInt16, math.MaxInt16
	assert.Equal(t, int16(math.MinInt16), PtrToInt2(&minInt16).Int16)
	assert.Equal(t, int16(math.MaxInt16), PtrToInt2(&maxInt16).Int16)

	minInt32, maxInt32 := math.MinInt32, math.MaxInt32
	assert.Equal(t, int32(math.MinInt32), PtrToInt4(&minInt32).Int32)
	assert.Equal(t, int32(math.MaxInt32), PtrToInt4(&maxInt32).Int32)

	minInt64, maxInt64 := int64(math.MinInt64), int64(math.MaxInt64)
	assert.Equal(t, int64(math.MinInt64), PtrToInt8(&minInt64).Int64)
	assert.Equal(t, int64(math.MaxInt64), PtrToInt8(&maxInt64).Int64)

	pastInt32 := math.MaxInt32 + 1
	narrowed := PtrToInt4(&pastInt32)
	assert.True(t, narrowed.Valid)
	assert.Equal(t, int32(math.MinInt32), narrowed.Int32)
}

func TestPtrToTextNamedString(t *testing.T) {
	t.Parallel()

	type code string
	v := code("PPG")
	got := PtrToText(&v)
	assert.True(t, got.Valid)
	assert.Equal(t, "PPG", got.String)
}
