package draftrank

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLeague(t *testing.T) {
	key, err := ParseLeague(" 465.l.1001 ", 0)
	require.NoError(t, err)
	assert.Equal(t, LeagueRef{LeagueKey: "465.l.1001"}, key)

	id, err := ParseLeague("1002", 2026)
	require.NoError(t, err)
	assert.Equal(t, LeagueRef{Season: 2026, LeagueID: 1002}, id)
	assert.True(t, id.resolved())

	for _, bad := range []struct {
		value  string
		season int
	}{{"", 2026}, {"465.l.x", 2026}, {"465.l.0", 2026}, {"abc", 2026}, {"-3", 2026}, {"1001", 0}} {
		_, err := ParseLeague(bad.value, bad.season)
		assert.ErrorIs(t, err, ErrUnknownLeague, "%q season %d", bad.value, bad.season)
	}
}
