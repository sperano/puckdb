package yahookey

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeysRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		parse func(string) (fmtStringer, error)
	}{
		{"league", "465.l.1001", parseLeague},
		{"team", "465.l.1001.t.12", parseTeam},
		{"player", "465.p.7520", parsePlayer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			key, err := test.parse(test.value)
			require.NoError(t, err)
			assert.Equal(t, test.value, key.String())
		})
	}
}

func TestKeyParsersRejectMalformedKeysConsistently(t *testing.T) {
	t.Parallel()
	overflow := strconv.FormatInt(math.MaxInt32+1, 10)
	tests := []struct {
		name  string
		parse func(string) (fmtStringer, error)
		bad   []string
	}{
		{"league", parseLeague, []string{"", "465", "abc.l.5", "465.x.5", "465.l.0", "0465.l.5", "465.l.05", "465.l." + overflow}},
		{"team", parseTeam, []string{"", "465.l.5", "abc.l.5.t.1", "465.l.5.x.1", "465.l.5.t.0", "465.l.5.t.01", "465.l.5.t." + overflow}},
		{"player", parsePlayer, []string{"", "465.p", "abc.p.5", "465.x.5", "465.p.0", "0465.p.5", "465.p.05", "465.p." + overflow}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, value := range test.bad {
				_, err := test.parse(value)
				assert.Error(t, err, "%q", value)
			}
		})
	}
}

type fmtStringer interface {
	String() string
}

func parseLeague(value string) (fmtStringer, error) {
	return ParseLeagueKey(value)
}

func parseTeam(value string) (fmtStringer, error) {
	return ParseTeamKey(value)
}

func parsePlayer(value string) (fmtStringer, error) {
	return ParsePlayerKey(value)
}
