package yahoolink

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLeagueIDs(t *testing.T) {
	cases := []struct {
		name string
		list string
		want []int32
	}{
		{"empty means every league", "", nil},
		{"blanks and trailing comma are ignored", " 29249, ,1001, ", []int32{29249, 1001}},
		{"duplicates collapse", "29249,29249", []int32{29249}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseLeagueIDs(c.list)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestParseLeagueIDsRejects(t *testing.T) {
	for _, bad := range []string{"abc", "0", "-5", "465.l.1001"} {
		_, err := ParseLeagueIDs(bad)
		assert.Error(t, err, bad)
	}
}
