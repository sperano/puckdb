package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEscapeLikePattern pins B9: user input must be matched literally so a
// term like "%" cannot wildcard-match the whole players table.
func TestEscapeLikePattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain name is untouched", "mcdavid", "mcdavid"},
		{"percent is escaped", "%", `\%`},
		{"underscore is escaped", "a_b", `a\_b`},
		{"backslash is escaped first", `a\b`, `a\\b`},
		{"combo", `%_\`, `\%\_\\`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, escapeLikePattern(tc.in))
		})
	}
}

// TestClampListLimit pins B9: client-supplied limits above maxListLimit are
// capped, while nil / non-positive limits fall through to the query default.
func TestClampListLimit(t *testing.T) {
	t.Parallel()

	t.Run("nil passes through", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, clampListLimit(nil))
	})

	t.Run("non-positive passes through unchanged", func(t *testing.T) {
		t.Parallel()
		zero := 0
		neg := -5
		assert.Equal(t, &zero, clampListLimit(&zero))
		assert.Equal(t, &neg, clampListLimit(&neg))
	})

	t.Run("within cap is unchanged", func(t *testing.T) {
		t.Parallel()
		v := 50
		assert.Equal(t, 50, *clampListLimit(&v))
	})

	t.Run("at cap is unchanged", func(t *testing.T) {
		t.Parallel()
		v := maxListLimit
		assert.Equal(t, maxListLimit, *clampListLimit(&v))
	})

	t.Run("above cap is clamped", func(t *testing.T) {
		t.Parallel()
		v := maxListLimit + 1_000_000
		assert.Equal(t, maxListLimit, *clampListLimit(&v))
	})
}
