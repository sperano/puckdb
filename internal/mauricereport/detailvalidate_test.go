package mauricereport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRowIDAcceptsOnlyCanonicalPositiveIDs(t *testing.T) {
	id, err := parseRowID("callId", "42")
	require.NoError(t, err)
	assert.Equal(t, int64(42), id)
	for _, bad := range []string{"", "0", "-1", "+1", "042", " 42", "4.2", "9223372036854775808", "x"} {
		_, err := parseRowID("callId", bad)
		assert.EqualError(t, err, "invalid argument: callId is not an ID", bad)
	}
}

func TestPayloadWindowDefaultsAndBounds(t *testing.T) {
	offset, limit, err := payloadWindow(PayloadRequest{Offset: 7})
	require.NoError(t, err)
	assert.Equal(t, int64(7), offset)
	assert.Equal(t, DefaultPayloadRunes, limit)
	_, limit, err = payloadWindow(PayloadRequest{Offset: MaxPayloadOffset, Limit: MaxPayloadRunes})
	require.NoError(t, err)
	assert.Equal(t, MaxPayloadRunes, limit)

	tests := []struct {
		req  PayloadRequest
		want string
	}{
		{PayloadRequest{Offset: -1}, "invalid argument: offset -1 is outside 0..1073741824"},
		{PayloadRequest{Offset: MaxPayloadOffset + 1}, "invalid argument: offset 1073741825 is outside 0..1073741824"},
		{PayloadRequest{Limit: -1}, "invalid argument: limit -1 is outside 1..262144"},
		{PayloadRequest{Limit: MaxPayloadRunes + 1}, "invalid argument: limit 262145 is outside 1..262144"},
	}
	for _, tt := range tests {
		_, _, err := payloadWindow(tt.req)
		assert.EqualError(t, err, tt.want)
	}
}

func TestTextWindowEndAndTruncation(t *testing.T) {
	window := Text{Text: "é🏒", Offset: 3, Length: 6}
	assert.Equal(t, int64(5), window.End(), "counted in code points, not bytes")
	assert.True(t, window.Truncated())
	last := Text{Text: "a", Offset: 5, Length: 6}
	assert.False(t, last.Truncated())
	empty := Text{Offset: 9, Length: 6}
	assert.False(t, empty.Truncated(), "past the end")
}

func TestDetailScopeSeparatesParents(t *testing.T) {
	r, err := validateRef(ConversationRef{Instance: instanceFull, ConversationID: "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001"})
	require.NoError(t, err)
	turn, err := detailScope(r, "a")
	require.NoError(t, err)
	other, err := detailScope(r, "b")
	require.NoError(t, err)
	assert.NotEqual(t, turn, other)
	r.includeDeleted = true
	deleted, err := detailScope(r, "a")
	require.NoError(t, err)
	assert.Equal(t, turn, deleted, "deletion does not change a conversation's rows")
}
