package mauricereport

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testScope is the scope of the cursors the tests decode.
const testScope = "scope"

func TestCursorRoundTripKeepsMicroseconds(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 5, 12, 30, 0, 123456000, time.UTC)
	id := "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001"
	encoded, err := keyCursor(cursorConversations, testScope, at, id)
	require.NoError(t, err)
	got, err := decodeCursor(cursorConversations, testScope, encoded)
	require.NoError(t, err)
	assert.True(t, got.At.Equal(at), "got %s", got.At)
	assert.Equal(t, id, got.ID.String())
}

func TestDecodeCursorEmptyIsFirstPage(t *testing.T) {
	t.Parallel()
	got, err := decodeCursor(cursorUsers, testScope, "")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDecodeCursorRejectsForeignAndMalformedCursors(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	id := uuid.MustParse("0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001")
	raw := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	usersCursor, err := encodeCursor(cursor{Kind: cursorUsers, Scope: testScope, At: &at, ID: &id})
	require.NoError(t, err)
	otherScope, err := encodeCursor(cursor{Kind: cursorConversations, Scope: "other", At: &at, ID: &id})
	require.NoError(t, err)
	noScope, err := encodeCursor(cursor{Kind: cursorConversations, At: &at, ID: &id})
	require.NoError(t, err)
	instanceCursor, err := encodeCursor(cursor{Kind: cursorInstances, Name: "maurice"})
	require.NoError(t, err)

	tests := map[string]string{
		"another list's cursor": usersCursor,
		"another scope":         otherScope,
		"no scope":              noScope,
		"instance cursor":       instanceCursor,
		"not base64":            "not a cursor!",
		"not JSON":              raw("hello"),
		"other version":         raw(`{"v":2,"k":"conversations","t":"2026-11-05T00:00:00Z","id":"0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001"}`),
		"missing id":            raw(`{"v":1,"k":"conversations","t":"2026-11-05T00:00:00Z"}`),
		"bad id":                raw(`{"v":1,"k":"conversations","t":"2026-11-05T00:00:00Z","id":"x"}`),
		"extra name":            raw(`{"v":1,"k":"conversations","n":"x","t":"2026-11-05T00:00:00Z","id":"0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001"}`),
		"too long":              strings.Repeat("A", MaxCursorBytes+1),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeCursor(cursorConversations, testScope, encoded)
			assert.ErrorIs(t, err, ErrInvalidCursor)
		})
	}
}

func TestDecodeInstanceCursorNeedsAName(t *testing.T) {
	t.Parallel()
	encoded := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"k":"instances"}`))
	_, err := decodeCursor(cursorInstances, "", encoded)
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestUsersScopeDependsOnTheInstance(t *testing.T) {
	t.Parallel()
	a, err := usersScope(instanceFull)
	require.NoError(t, err)
	again, err := usersScope(instanceFull)
	require.NoError(t, err)
	b, err := usersScope(instanceCrapettes)
	require.NoError(t, err)
	assert.Equal(t, a, again)
	assert.NotEqual(t, a, b)
}

// A cursor scoped to the longest valid search still fits MaxCursorBytes.
func TestCursorWithLongestSearchFitsTheBound(t *testing.T) {
	t.Parallel()
	q, err := validateFilter(ConversationFilter{Instance: strings.Repeat("é", MaxNameRunes),
		Search: strings.Repeat("é", MaxSearchRunes)})
	require.NoError(t, err)
	scope, err := q.scope()
	require.NoError(t, err)
	encoded, err := keyCursor(cursorConversations, scope, time.Now(), "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001")
	require.NoError(t, err)
	_, err = decodeCursor(cursorConversations, scope, encoded)
	require.NoError(t, err)
}
