package mauricereport

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	instanceFull      = "maurice"
	instanceCrapettes = "maurice-crapettes"
	instanceMain      = "puckdb"
)

// t0 is the fixtures' reference instant.
var t0 = time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)

// openStore empties the Maurice tables (app_users cascades to sessions,
// conversations, turns, messages and calls) and returns a store and a seeder
// over the shared test database.
func openStore(t *testing.T) (*PGStore, *mauricefixtures.Seeder) {
	t.Helper()
	pool := pgtest.Open(t, "app_users")
	return NewPGStore(pool), mauricefixtures.NewSeeder(t, pool)
}

func TestInstancesPagesByNameWithUserCounts(t *testing.T) {
	store, seed := openStore(t)
	ctx := context.Background()
	for i, instance := range []string{instanceMain, instanceCrapettes, instanceFull, instanceCrapettes} {
		seed.User(mauricefixtures.User{Instance: instance, Username: "user" + string(rune('a'+i)), FirstSeenAt: t0})
	}

	first, err := store.Instances(ctx, PageRequest{Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []Instance{{instanceFull, 1}, {instanceCrapettes, 2}}, first.Instances)
	require.NotEmpty(t, first.NextCursor)

	second, err := store.Instances(ctx, PageRequest{Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, []Instance{{instanceMain, 1}}, second.Instances)
	assert.Empty(t, second.NextCursor, "last page")
}

func TestInstancesWithoutUsersIsAnEmptyPage(t *testing.T) {
	store, _ := openStore(t)
	page, err := store.Instances(context.Background(), PageRequest{})
	require.NoError(t, err)
	assert.Empty(t, page.Instances)
	assert.Empty(t, page.NextCursor)
}

// Users sharing first_seen_at are ordered by ID and each appears on exactly
// one page; another instance's user, the same identity included, on none.
func TestUsersPagesOneInstanceWithTies(t *testing.T) {
	store, seed := openStore(t)
	ctx := context.Background()
	older := seed.User(mauricefixtures.User{Instance: instanceCrapettes, Username: "old", FirstSeenAt: t0.Add(-time.Hour)})
	tied := []string{
		seed.User(mauricefixtures.User{Instance: instanceCrapettes, Username: "a", FirstSeenAt: t0}),
		seed.User(mauricefixtures.User{Instance: instanceCrapettes, Username: "b", FirstSeenAt: t0}),
		seed.User(mauricefixtures.User{Instance: instanceCrapettes, Subject: "eric-uid", Username: "eric", FirstSeenAt: t0}),
	}
	seed.User(mauricefixtures.User{Instance: instanceFull, Subject: "eric-uid", Username: "eric", FirstSeenAt: t0.Add(time.Hour)})
	sort.Sort(sort.Reverse(sort.StringSlice(tied)))
	want := append(tied, older)

	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, len(want), "paging must end")
		page, err := store.Users(ctx, instanceCrapettes, PageRequest{Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, u := range page.Users {
			assert.Equal(t, instanceCrapettes, u.Instance)
			got = append(got, u.ID)
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	assert.Equal(t, want, got, "newest first_seen_at first, ties by ID descending")
}

func TestUsersReturnsProfileMetadata(t *testing.T) {
	store, seed := openStore(t)
	profileUpdated := t0.Add(time.Hour)
	disabled := t0.Add(2 * time.Hour)
	id := seed.User(mauricefixtures.User{Instance: instanceCrapettes, Provider: "cloudflare", Subject: "sub-1",
		Username: "eric@example.com", DisplayName: "Eric", Email: "eric@example.com", FirstSeenAt: t0,
		ProfileUpdatedAt: profileUpdated, DisabledAt: &disabled})
	page, err := store.Users(context.Background(), instanceCrapettes, PageRequest{})
	require.NoError(t, err)
	require.Len(t, page.Users, 1)
	u := page.Users[0]
	assert.True(t, u.FirstSeenAt.Equal(t0), "first_seen_at: got %s", u.FirstSeenAt)
	assert.True(t, u.ProfileUpdatedAt.Equal(profileUpdated), "profile_updated_at: got %s", u.ProfileUpdatedAt)
	require.NotNil(t, u.DisabledAt)
	assert.True(t, u.DisabledAt.Equal(disabled), "disabled_at: got %s", u.DisabledAt)
	// The instants are checked above; normalize their location for Equal.
	u.FirstSeenAt, u.ProfileUpdatedAt, u.DisabledAt = t0, profileUpdated, &disabled
	email, displayName := "eric@example.com", "Eric"
	assert.Equal(t, User{ID: id, Instance: instanceCrapettes, AuthProvider: "cloudflare", Username: email,
		DisplayName: &displayName, Email: &email, FirstSeenAt: t0, ProfileUpdatedAt: profileUpdated,
		DisabledAt: &disabled}, u)
}

func TestUsersWithoutOptionalProfileFieldsAreNull(t *testing.T) {
	store, seed := openStore(t)
	seed.User(mauricefixtures.User{Instance: instanceCrapettes, Username: "eric", FirstSeenAt: t0})
	page, err := store.Users(context.Background(), instanceCrapettes, PageRequest{})
	require.NoError(t, err)
	require.Len(t, page.Users, 1)
	assert.Nil(t, page.Users[0].DisplayName)
	assert.Nil(t, page.Users[0].Email)
	assert.Nil(t, page.Users[0].DisabledAt)
}

func TestUsersOfAnInstanceWithoutUsersIsAnEmptyPage(t *testing.T) {
	store, seed := openStore(t)
	seed.User(mauricefixtures.User{Instance: instanceFull, Username: "eric", FirstSeenAt: t0})
	page, err := store.Users(context.Background(), "nobody-here", PageRequest{})
	require.NoError(t, err)
	assert.Empty(t, page.Users)
	assert.Empty(t, page.NextCursor)
}

// twoInstances seeds the same Authentik identity on two instances, each
// user with one conversation, and returns the users and conversations.
type twoInstances struct {
	fullUser, crapettesUser, fullConv, crapettesConv string
}

func seedTwoInstances(seed *mauricefixtures.Seeder) twoInstances {
	var w twoInstances
	w.fullUser = seed.User(mauricefixtures.User{Instance: instanceFull, Subject: "eric-uid", Username: "eric", FirstSeenAt: t0})
	w.crapettesUser = seed.User(mauricefixtures.User{Instance: instanceCrapettes, Subject: "eric-uid", Username: "eric", FirstSeenAt: t0})
	w.fullConv = seed.Conversation(mauricefixtures.Conversation{UserID: w.fullUser, Title: "full", CreatedAt: t0})
	w.crapettesConv = seed.Conversation(mauricefixtures.Conversation{UserID: w.crapettesUser, Title: "crapettes", CreatedAt: t0})
	return w
}

func conversationIDs(page ConversationPage) []string {
	ids := make([]string, len(page.Conversations))
	for i, c := range page.Conversations {
		ids[i] = c.ID
	}
	return ids
}

func TestConversationsAreScopedToTheSelectedInstance(t *testing.T) {
	store, seed := openStore(t)
	ctx := context.Background()
	w := seedTwoInstances(seed)

	page, err := store.Conversations(ctx, ConversationFilter{Instance: instanceCrapettes}, PageRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{w.crapettesConv}, conversationIDs(page))
	assert.Equal(t, instanceCrapettes, page.Conversations[0].Instance)
	assert.Equal(t, w.crapettesUser, page.Conversations[0].UserID)

	page, err = store.Conversations(ctx, ConversationFilter{Instance: instanceFull, UserID: w.fullUser}, PageRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{w.fullConv}, conversationIDs(page))

	page, err = store.Conversations(ctx, ConversationFilter{Instance: "unknown"}, PageRequest{})
	require.NoError(t, err)
	assert.Empty(t, page.Conversations, "an unknown instance is an empty page")
}

func TestConversationsRefuseAnotherInstancesUser(t *testing.T) {
	store, seed := openStore(t)
	w := seedTwoInstances(seed)
	for _, userID := range []string{w.fullUser, "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001"} {
		page, err := store.Conversations(context.Background(),
			ConversationFilter{Instance: instanceCrapettes, UserID: userID}, PageRequest{})
		require.ErrorIs(t, err, ErrUserNotInInstance, "user %s", userID)
		assert.Empty(t, page.Conversations)
	}
}

func TestConversationLookupIsScopedToTheSelectedInstance(t *testing.T) {
	store, seed := openStore(t)
	ctx := context.Background()
	w := seedTwoInstances(seed)

	_, found, err := store.Conversation(ctx, instanceCrapettes, w.fullConv, true)
	require.NoError(t, err)
	assert.False(t, found, "another instance's conversation, even with includeDeleted")

	_, found, err = store.Conversation(ctx, instanceCrapettes, "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001", false)
	require.NoError(t, err)
	assert.False(t, found, "no such conversation")

	c, found, err := store.Conversation(ctx, instanceFull, w.fullConv, false)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, w.fullConv, c.ID)
	assert.Equal(t, w.fullUser, c.UserID)
}

func TestDeletedConversationsAreExcludedUnlessIncluded(t *testing.T) {
	store, seed := openStore(t)
	ctx := context.Background()
	user := seed.User(mauricefixtures.User{Instance: instanceFull, Username: "eric", FirstSeenAt: t0})
	kept := seed.Conversation(mauricefixtures.Conversation{UserID: user, CreatedAt: t0})
	deletedAt := t0.Add(2 * time.Hour)
	deleted := seed.Conversation(mauricefixtures.Conversation{UserID: user, CreatedAt: t0.Add(time.Hour), DeletedAt: &deletedAt})

	page, err := store.Conversations(ctx, ConversationFilter{Instance: instanceFull}, PageRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{kept}, conversationIDs(page))

	page, err = store.Conversations(ctx, ConversationFilter{Instance: instanceFull, IncludeDeleted: true}, PageRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{deleted, kept}, conversationIDs(page))
	require.NotNil(t, page.Conversations[0].DeletedAt)
	assert.True(t, page.Conversations[0].DeletedAt.Equal(deletedAt))
	assert.Nil(t, page.Conversations[1].DeletedAt)

	_, found, err := store.Conversation(ctx, instanceFull, deleted, false)
	require.NoError(t, err)
	assert.False(t, found, "deleted is not found by default")
	_, found, err = store.Conversation(ctx, instanceFull, deleted, true)
	require.NoError(t, err)
	assert.True(t, found)
}

// Conversations sharing created_at are ordered by ID and each appears on
// exactly one page.
func TestConversationsPageStablyThroughTies(t *testing.T) {
	store, seed := openStore(t)
	ctx := context.Background()
	user := seed.User(mauricefixtures.User{Instance: instanceFull, Username: "eric", FirstSeenAt: t0})
	newest := seed.Conversation(mauricefixtures.Conversation{UserID: user, CreatedAt: t0.Add(time.Hour)})
	var tied []string
	for range 4 {
		tied = append(tied, seed.Conversation(mauricefixtures.Conversation{UserID: user, CreatedAt: t0}))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(tied)))
	want := append([]string{newest}, tied...)

	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, len(want), "paging must end")
		page, err := store.Conversations(ctx, ConversationFilter{Instance: instanceFull}, PageRequest{Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		assert.LessOrEqual(t, len(page.Conversations), 2)
		got = append(got, conversationIDs(page)...)
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	assert.Equal(t, want, got)
}

func TestConversationsDefaultPageSize(t *testing.T) {
	store, seed := openStore(t)
	user := seed.User(mauricefixtures.User{Instance: instanceFull, Username: "eric", FirstSeenAt: t0})
	for i := range DefaultLimit + 1 {
		seed.Conversation(mauricefixtures.Conversation{UserID: user, CreatedAt: t0.Add(time.Duration(i) * time.Minute)})
	}
	page, err := store.Conversations(context.Background(), ConversationFilter{Instance: instanceFull}, PageRequest{})
	require.NoError(t, err)
	assert.Len(t, page.Conversations, DefaultLimit)
	assert.NotEmpty(t, page.NextCursor)

	exact, err := store.Conversations(context.Background(), ConversationFilter{Instance: instanceFull},
		PageRequest{Limit: DefaultLimit + 1})
	require.NoError(t, err)
	assert.Len(t, exact.Conversations, DefaultLimit+1)
	assert.Empty(t, exact.NextCursor, "a page holding the last row has no cursor")
}
