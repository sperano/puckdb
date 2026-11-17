package yahoolink

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultSeason(t *testing.T) {
	const current = 2026
	cases := map[string]struct {
		seasons []int32
		want    int32
		found   bool
	}{
		"none":                       {nil, 0, false},
		"current only":               {[]int32{2026, 2026}, 2026, true},
		"current and past":           {[]int32{2024, 2026, 2025}, 2026, true},
		"off-season falls back":      {[]int32{2023, 2025}, 2025, true},
		"future season skipped":      {[]int32{2027, 2025}, 2025, true},
		"only future seasons":        {[]int32{2027, 2028}, 0, false},
		"future and current present": {[]int32{2027, 2026}, 2026, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, found := DefaultSeason(c.seasons, current)
			assert.Equal(t, c.found, found)
			assert.Equal(t, c.want, got)
		})
	}
}
