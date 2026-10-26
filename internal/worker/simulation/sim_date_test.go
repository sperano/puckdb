package simulation

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
)

func TestAddDays_CalendarRollover(t *testing.T) {
	cases := []struct {
		base string
		n    int
		want string
	}{
		{"2024-11-15", 1, "2024-11-16"},
		{"2024-10-31", 1, "2024-11-01"}, // month end
		{"2024-12-31", 1, "2025-01-01"}, // year end
		{"2024-02-28", 1, "2024-02-29"}, // leap day
		{"2025-02-28", 1, "2025-03-01"}, // no leap day
		{"2024-03-09", 1, "2024-03-10"}, // US DST start: still one calendar day
		{"2024-11-15", 2, "2024-11-17"}, // waiver processing offset
		{"2024-11-15", 0, "2024-11-15"},
	}
	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			assert.Equal(t, pgDate(t, tc.want), addDays(pgDate(t, tc.base), tc.n))
		})
	}
}

// The day loop and the waiver processing date both rely on a NULL date
// staying NULL rather than becoming year 1 plus n days.
func TestAddDays_InvalidStaysInvalid(t *testing.T) {
	assert.Equal(t, pgtype.Date{}, addDays(pgtype.Date{}, 1))
}
