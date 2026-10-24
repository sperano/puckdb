package draft

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLeagueKey(t *testing.T) {
	gameKey, leagueID, err := ParseLeagueKey("465.l.1001")
	require.NoError(t, err)
	assert.Equal(t, 465, gameKey)
	assert.Equal(t, 1001, leagueID)

	for _, bad := range []string{"", "465", "465.l", "465.x.1001", "465.l.1001.2", "abc.l.1001", "465.l.x",
		"0.l.1001", "465.l.0", "465.l.-1", "+465.l.1001", "465.l.+1001", "0465.l.1001", "465.l.01001", " 465.l.1001", "465.l." + strconv.FormatInt(math.MaxInt32+1, 10)} {
		_, _, err := ParseLeagueKey(bad)
		assert.ErrorIs(t, err, ErrMalformedLeagueRef, "%q", bad)
	}
}

func TestParseLeagueID(t *testing.T) {
	id, err := ParseLeagueID("1001")
	require.NoError(t, err)
	assert.Equal(t, 1001, id)
	id, err = ParseLeagueID(strconv.Itoa(math.MaxInt32))
	require.NoError(t, err)
	assert.Equal(t, math.MaxInt32, id)

	for _, bad := range []string{"", "abc", "0", "-3", "1.5", "+1001", "01001", " 1001", strconv.FormatInt(math.MaxInt32+1, 10)} {
		_, err := ParseLeagueID(bad)
		assert.ErrorIs(t, err, ErrMalformedLeagueRef, "%q", bad)
	}
}

func TestIsLeagueKeyTellsTheTwoReferenceForms(t *testing.T) {
	assert.True(t, IsLeagueKey("465.l.1001"))
	assert.True(t, IsLeagueKey("465.x.1001"), "a malformed key is still a key, rejected by ParseLeagueKey")
	assert.False(t, IsLeagueKey("1001"))
	assert.False(t, IsLeagueKey("abc"))
}
