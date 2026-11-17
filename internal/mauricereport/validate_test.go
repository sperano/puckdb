package mauricereport

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testInstance = "maurice"

func TestValidateFilterRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	valid := ConversationFilter{Instance: testInstance}
	tests := []struct {
		name   string
		edit   func(*ConversationFilter)
		wantIn string
	}{
		{"missing instance", func(f *ConversationFilter) { f.Instance = "" }, "instance is required"},
		{"padded instance", func(f *ConversationFilter) { f.Instance = " maurice" }, "instance has leading or trailing spaces"},
		{"instance too long", func(f *ConversationFilter) { f.Instance = strings.Repeat("i", MaxNameRunes+1) }, "instance is 201 characters, at most 200"},
		{"instance with NUL", func(f *ConversationFilter) { f.Instance = "mau\x00rice" }, "instance is not valid text"},
		{"user not a UUID", func(f *ConversationFilter) { f.UserID = "eric" }, "userId is not a UUID"},
		{"user in braces", func(f *ConversationFilter) { f.UserID = "{0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001}" }, "userId is not a UUID"},
		{"from not a date", func(f *ConversationFilter) { f.From = "2026-1-5" }, `from "2026-1-5" is not a YYYY-MM-DD date`},
		{"to out of month", func(f *ConversationFilter) { f.To = "2026-02-30" }, `to "2026-02-30" is not a YYYY-MM-DD date`},
		{"from with time", func(f *ConversationFilter) { f.From = "2026-11-05T00:00:00Z" }, "is not a YYYY-MM-DD date"},
		{"to before from", func(f *ConversationFilter) { f.From, f.To = "2026-11-05", "2026-11-04" }, "to 2026-11-04 is before from 2026-11-05"},
		{"unknown status", func(f *ConversationFilter) { f.Statuses = []TurnStatus{"done"} }, `unknown turn status "done"`},
		{"padded model", func(f *ConversationFilter) { f.Model = "claude " }, "model has leading or trailing spaces"},
		{"provider too long", func(f *ConversationFilter) { f.Provider = strings.Repeat("p", MaxNameRunes+1) }, "provider is 201 characters"},
		{"tool with NUL", func(f *ConversationFilter) { f.ToolName = "get\x00" }, "toolName is not valid text"},
		{"search too long", func(f *ConversationFilter) { f.Search = strings.Repeat("é", MaxSearchRunes+1) }, "search is 201 characters, at most 200"},
		{"search not UTF-8", func(f *ConversationFilter) { f.Search = "\xff" }, "search is not valid text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := valid
			tt.edit(&f)
			_, err := validateFilter(f)
			require.ErrorIs(t, err, ErrInvalidArgument)
			assert.Contains(t, err.Error(), tt.wantIn)
		})
	}
}

func TestValidateFilterAcceptsTheBounds(t *testing.T) {
	t.Parallel()
	q, err := validateFilter(ConversationFilter{
		Instance: strings.Repeat("i", MaxNameRunes),
		UserID:   "0B5F3A52-8A4B-4A52-9D55-2A6FB3E6A001",
		From:     "2026-11-05", To: "2026-11-05",
		Statuses: []TurnStatus{TurnFailed, TurnCancelled, TurnFailed},
		Search:   strings.Repeat("é", MaxSearchRunes),
	})
	require.NoError(t, err)
	assert.Equal(t, "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001", q.userID.String())
	assert.Equal(t, time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), *q.from)
	assert.Equal(t, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), *q.until, "to is inclusive: until the next day")
	assert.Equal(t, []string{"failed", "cancelled"}, q.statuses, "deduplicated, in order")
	assert.True(t, q.hasTurnFilter())
}

func TestValidateFilterWithoutTurnFilters(t *testing.T) {
	t.Parallel()
	q, err := validateFilter(ConversationFilter{Instance: testInstance, Search: "habs"})
	require.NoError(t, err)
	assert.False(t, q.hasTurnFilter(), "search is not a turn filter")
}

func TestSearchPatternEscapesWildcards(t *testing.T) {
	t.Parallel()
	got, err := searchPattern("  50%_off\\now ")
	require.NoError(t, err)
	assert.Equal(t, `%50\%\_off\\now%`, got)

	blank, err := searchPattern(" \t ")
	require.NoError(t, err)
	assert.Empty(t, blank, "blank is no search")
}

func TestPageLimit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		limit, want int
	}{{0, DefaultLimit}, {1, 1}, {MaxLimit, MaxLimit}} {
		got, err := pageLimit(tt.limit)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got, "limit %d", tt.limit)
	}
	for _, limit := range []int{-1, MaxLimit + 1} {
		_, err := pageLimit(limit)
		require.ErrorIs(t, err, ErrInvalidArgument)
		assert.EqualError(t, err, "invalid argument: limit "+strconv.Itoa(limit)+" is outside 1..200")
	}
}

// The store validates every input before touching the database: a nil pool
// would panic otherwise.
func TestStoreRejectsInvalidInputBeforeQuerying(t *testing.T) {
	t.Parallel()
	s := NewPGStore(nil)
	ctx := context.Background()
	usersCursor, err := keyCursor(cursorUsers, time.Now(), "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001")
	require.NoError(t, err)

	_, err = s.Instances(ctx, PageRequest{Limit: MaxLimit + 1})
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = s.Instances(ctx, PageRequest{Cursor: usersCursor})
	assert.ErrorIs(t, err, ErrInvalidCursor, "a users cursor on the instance list")
	_, err = s.Users(ctx, "", PageRequest{})
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = s.Users(ctx, testInstance, PageRequest{Cursor: "%%%"})
	assert.ErrorIs(t, err, ErrInvalidCursor)
	_, err = s.Conversations(ctx, ConversationFilter{Instance: testInstance}, PageRequest{Cursor: usersCursor})
	assert.ErrorIs(t, err, ErrInvalidCursor, "a users cursor on the conversation list")
	_, err = s.Conversations(ctx, ConversationFilter{Instance: testInstance, From: "yesterday"}, PageRequest{})
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = s.Conversation(ctx, testInstance, "not-a-uuid", false)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = s.Conversation(ctx, "", "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001", false)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}
