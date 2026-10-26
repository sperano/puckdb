package graph

import (
	"strconv"
	"testing"
)

func TestConvertRows(t *testing.T) {
	t.Run("preserves order and applies typed conversion", func(t *testing.T) {
		got := convertRows([]int{3, 1, 2}, func(value int) *string {
			converted := strconv.Itoa(value)
			return &converted
		})

		want := []string{"3", "1", "2"}
		if len(got) != len(want) {
			t.Fatalf("got %d rows, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] == nil || *got[i] != want[i] {
				t.Errorf("row %d = %v, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("nil input returns non-nil empty slice", func(t *testing.T) {
		got := convertRows([]int(nil), func(value int) *int { return &value })
		if got == nil {
			t.Fatal("got nil slice, want non-nil empty slice")
		}
		if len(got) != 0 {
			t.Fatalf("got %d rows, want 0", len(got))
		}
	})
}

func TestOptionalString(t *testing.T) {
	t.Run("empty string returns nil", func(t *testing.T) {
		if got := optionalString(""); got != nil {
			t.Errorf("got %q, want nil", *got)
		}
	})
	t.Run("non-empty returns pointer to value", func(t *testing.T) {
		got := optionalString("hello")
		if got == nil || *got != "hello" {
			t.Errorf("got %v, want pointer to hello", got)
		}
	})
	t.Run("whitespace is non-empty", func(t *testing.T) {
		got := optionalString("   ")
		if got == nil || *got != "   " {
			t.Errorf("got %v, want pointer to whitespace", got)
		}
	})
}
