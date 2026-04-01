package worker

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
)

func TestParsePlayerLandingID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		wantID int64
		wantOK bool
	}{
		{"valid filename", "player-8478402-landing", 8478402, true},
		{"another valid ID", "player-123-landing", 123, true},
		{"missing prefix", "goalie-8478402-landing", 0, false},
		{"missing suffix", "player-8478402-stats", 0, false},
		{"non-numeric ID", "player-abc-landing", 0, false},
		{"too few parts", "player-landing", 0, false},
		{"empty string", "", 0, false},
		{"extra hyphens in middle", "player-847-840-2-landing", 847, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := parsePlayerLandingID(tt.input)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantID, id)
		})
	}
}

func TestIntPtrToInt4(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer", func(t *testing.T) {
		result := intPtrToInt4(nil)
		assert.Equal(t, pgtype.Int4{}, result)
		assert.False(t, result.Valid)
	})

	t.Run("zero value", func(t *testing.T) {
		v := 0
		result := intPtrToInt4(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int32(0), result.Int32)
	})

	t.Run("positive value", func(t *testing.T) {
		v := 42
		result := intPtrToInt4(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int32(42), result.Int32)
	})

	t.Run("negative value", func(t *testing.T) {
		v := -5
		result := intPtrToInt4(&v)
		assert.True(t, result.Valid)
		assert.Equal(t, int32(-5), result.Int32)
	})
}
