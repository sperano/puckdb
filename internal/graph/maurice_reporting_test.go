package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/graph/generated"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/maurice"
	"github.com/sperano/puckdb/internal/mauricereport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newReportingServer serves the schema the way the api command does: the
// instance's foreign-header filter, HeaderContext, and every directive,
// with an admin token configured so a token-only request reaches @admin's
// token path.
func newReportingServer(t *testing.T, reporting ReportingAuth, reader MauriceReportingReader) *httptest.Server {
	t.Helper()
	var provider appuser.Provider = appuser.AuthentikProvider{}
	if reporting.Provider == appuser.ProviderCloudflare {
		cf, err := appuser.NewCloudflareProvider(appuser.CloudflareAccessConfig{
			TeamDomain: "team.cloudflareaccess.com", Audience: "aud"})
		require.NoError(t, err)
		provider = cf
	}
	schema := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers:  &Resolver{MauriceReporting: reader},
		Directives: NewDirectiveRoot(AdminAuth{Group: config.DefaultAdminGroup, Token: testAdminToken}, reporting),
	}))
	schema.AddTransport(transport.POST{})
	server := httptest.NewServer(appuser.IgnoreForeignHeaders(provider)(httpx.HeaderContext(schema)))
	t.Cleanup(server.Close)
	return server
}

// recordingReader fails the test on any read: authorization must refuse
// before the store is reached.
type recordingReader struct{ t *testing.T }

func (r recordingReader) Instances(context.Context, mauricereport.PageRequest) (mauricereport.InstancePage, error) {
	r.t.Error("Instances read")
	return mauricereport.InstancePage{}, nil
}

func (r recordingReader) Users(context.Context, string, mauricereport.PageRequest) (mauricereport.UserPage, error) {
	r.t.Error("Users read")
	return mauricereport.UserPage{}, nil
}

func (r recordingReader) Conversations(context.Context, mauricereport.ConversationFilter, mauricereport.PageRequest) (mauricereport.ConversationPage, error) {
	r.t.Error("Conversations read")
	return mauricereport.ConversationPage{}, nil
}

func (r recordingReader) Conversation(context.Context, string, string, bool) (mauricereport.Conversation, bool, error) {
	r.t.Error("Conversation read")
	return mauricereport.Conversation{}, false, nil
}

func (r recordingReader) Overview(context.Context, mauricereport.ConversationFilter) (mauricereport.Overview, error) {
	r.t.Error("Overview read")
	return mauricereport.Overview{}, nil
}

func (r recordingReader) UserActivity(context.Context, mauricereport.ConversationFilter, mauricereport.PageRequest) (mauricereport.UserActivityPage, error) {
	r.t.Error("UserActivity read")
	return mauricereport.UserActivityPage{}, nil
}

const reportingFieldsQuery = `{
	mauriceReportingInstances { items { name } }
	mauriceReportingUsers(instance: "maurice") { items { id } }
	mauriceReportingConversations(filter: {instance: "maurice"}) { items { id } }
	mauriceReportingConversation(instance: "maurice", id: "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001") { id }
	mauriceReportingOverview(filter: {instance: "maurice"}) { userCount }
	mauriceReportingUserActivity(filter: {instance: "maurice"}) { items { active } }
}`

func TestReportingFieldsRefuseUnauthorizedRequests(t *testing.T) {
	t.Parallel()
	cloudflare := enabledReporting
	cloudflare.Provider = appuser.ProviderCloudflare
	disabled := enabledReporting
	disabled.Enabled = false
	tests := []struct {
		name    string
		auth    ReportingAuth
		headers http.Header
		want    string
	}{
		{"unauthenticated", enabledReporting, nil, errReportingDenied.Error()},
		{"not an admin", enabledReporting, http.Header{"X-Authentik-Uid": {"bob-uid"}, "X-Authentik-Groups": {"users"}}, errReportingDenied.Error()},
		{"admin token only", enabledReporting, http.Header{"X-Admin-Token": {testAdminToken}}, errReportingDenied.Error()},
		{"public provider", cloudflare, adminHeaders(), errReportingDenied.Error()},
		{"feature off", disabled, adminHeaders(), errReportingDisabled.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := postGraphQL(t, newReportingServer(t, tt.auth, recordingReader{t}), tt.headers, reportingFieldsQuery, nil)
			assert.JSONEq(t, "null", string(resp.Data), "no field resolves")
			require.NotEmpty(t, resp.Errors)
			for _, e := range resp.Errors {
				assert.Equal(t, tt.want, e.Message)
			}
		})
	}
}

// reportingWorld is the same Authentik identity on the full and the public
// instance, each with conversations, plus another public user with none.
type reportingWorld struct {
	fullUser, publicUser, otherUser string
	fullConv, publicConv, deleted   string
}

var reportingT0 = time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)

func seedReportingWorld(seed *mauricefixtures.Seeder) reportingWorld {
	var w reportingWorld
	w.fullUser = seed.User(mauricefixtures.User{Instance: "maurice", Subject: "eric-uid", Username: "eric", FirstSeenAt: reportingT0})
	w.publicUser = seed.User(mauricefixtures.User{Instance: "maurice-crapettes", Subject: "eric-uid", Username: "eric", FirstSeenAt: reportingT0})
	w.otherUser = seed.User(mauricefixtures.User{Instance: "maurice-crapettes", Username: "bob", FirstSeenAt: reportingT0.Add(time.Hour)})
	w.fullConv = seed.Conversation(mauricefixtures.Conversation{UserID: w.fullUser, Title: "full", CreatedAt: reportingT0})
	w.publicConv = seed.Conversation(mauricefixtures.Conversation{UserID: w.publicUser, Title: "public", CreatedAt: reportingT0})
	turn := seed.Turn(mauricefixtures.Turn{ConversationID: w.publicConv, UserID: w.publicUser,
		Status: mauricefixtures.StatusCancelled, StartedAt: reportingT0})
	seed.Call(mauricefixtures.Call{ConversationID: w.publicConv, TurnID: turn, Provider: "anthropic", Model: "claude-sonnet-5-5", StartedAt: reportingT0})
	deletedAt := reportingT0.Add(time.Hour)
	w.deleted = seed.Conversation(mauricefixtures.Conversation{UserID: w.publicUser, Title: "gone",
		CreatedAt: reportingT0.Add(time.Minute), DeletedAt: &deletedAt})
	return w
}

func newReportingDB(t *testing.T) (*httptest.Server, *mauricefixtures.Seeder, maurice.DB) {
	t.Helper()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	server := newReportingServer(t, enabledReporting, mauricereport.NewPGStore(pool))
	return server, mauricefixtures.NewSeeder(t, pool), maurice.NewPgDB(pool)
}

// gqlData decodes a response's data, failing on GraphQL errors.
func gqlData[T any](t *testing.T, resp gqlResponse) T {
	t.Helper()
	require.Empty(t, resp.Errors)
	var out T
	require.NoError(t, json.Unmarshal(resp.Data, &out))
	return out
}

type conversationItem struct {
	ID                string  `json:"id"`
	Instance          string  `json:"instance"`
	UserID            string  `json:"userId"`
	Status            *string `json:"status"`
	LastTurnStartedAt *string `json:"lastTurnStartedAt"`
	DeletedAt         *string `json:"deletedAt"`
	Turns             struct {
		Total     int `json:"total"`
		Cancelled int `json:"cancelled"`
	} `json:"turns"`
	Models []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	} `json:"models"`
}

const conversationsQuery = `query($filter: MauriceReportingConversationFilter!, $limit: Int, $cursor: String) {
	mauriceReportingConversations(filter: $filter, limit: $limit, cursor: $cursor) {
		items { id instance userId status lastTurnStartedAt deletedAt turns { total cancelled } models { provider model } }
		nextCursor
	}
}`

type conversationsData struct {
	Page struct {
		Items      []conversationItem `json:"items"`
		NextCursor *string            `json:"nextCursor"`
	} `json:"mauriceReportingConversations"`
}

func TestReportingAdminSelectsAnotherInstance(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedReportingWorld(seed)

	instances := gqlData[struct {
		Page struct {
			Items []struct {
				Name      string `json:"name"`
				UserCount int    `json:"userCount"`
			} `json:"items"`
			NextCursor *string `json:"nextCursor"`
		} `json:"mauriceReportingInstances"`
	}](t, postGraphQL(t, server, adminHeaders(), `{ mauriceReportingInstances { items { name userCount } nextCursor } }`, nil))
	assert.Len(t, instances.Page.Items, 2)
	assert.Equal(t, "maurice", instances.Page.Items[0].Name)
	assert.Equal(t, 1, instances.Page.Items[0].UserCount)
	assert.Equal(t, "maurice-crapettes", instances.Page.Items[1].Name)
	assert.Equal(t, 2, instances.Page.Items[1].UserCount)
	assert.Nil(t, instances.Page.NextCursor)

	users := gqlData[struct {
		Page struct {
			Items []struct {
				ID       string `json:"id"`
				Instance string `json:"instance"`
			} `json:"items"`
		} `json:"mauriceReportingUsers"`
	}](t, postGraphQL(t, server, adminHeaders(), `{ mauriceReportingUsers(instance: "maurice-crapettes") { items { id instance } } }`, nil))
	require.Len(t, users.Page.Items, 2)
	assert.Equal(t, w.otherUser, users.Page.Items[0].ID, "newest first")
	assert.Equal(t, w.publicUser, users.Page.Items[1].ID)

	page := gqlData[conversationsData](t, postGraphQL(t, server, adminHeaders(), conversationsQuery,
		map[string]any{"filter": map[string]any{"instance": "maurice-crapettes", "statuses": []string{"CANCELLED"}}}))
	require.Len(t, page.Page.Items, 1)
	got := page.Page.Items[0]
	assert.Equal(t, w.publicConv, got.ID)
	assert.Equal(t, "maurice-crapettes", got.Instance)
	assert.Equal(t, w.publicUser, got.UserID)
	require.NotNil(t, got.Status)
	assert.Equal(t, "CANCELLED", *got.Status)
	assert.Equal(t, 1, got.Turns.Total)
	assert.Equal(t, 1, got.Turns.Cancelled)
	require.Len(t, got.Models, 1)
	assert.Equal(t, "claude-sonnet-5-5", got.Models[0].Model)
}

func TestReportingRefusesWrongInstanceSelectors(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedReportingWorld(seed)

	resp := postGraphQL(t, server, adminHeaders(), conversationsQuery,
		map[string]any{"filter": map[string]any{"instance": "maurice-crapettes", "userId": w.fullUser}})
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, mauricereport.ErrUserNotInInstance.Error(), resp.Errors[0].Message)
	assert.JSONEq(t, "null", string(resp.Data))

	lookup := `query($instance: String!, $id: String!) { mauriceReportingConversation(instance: $instance, id: $id, includeDeleted: true) { id } }`
	foreign := gqlData[map[string]any](t, postGraphQL(t, server, adminHeaders(), lookup,
		map[string]any{"instance": "maurice-crapettes", "id": w.fullConv}))
	assert.Equal(t, map[string]any{"mauriceReportingConversation": nil}, foreign, "another instance's conversation is not found")
	own := gqlData[map[string]map[string]string](t, postGraphQL(t, server, adminHeaders(), lookup,
		map[string]any{"instance": "maurice", "id": w.fullConv}))
	assert.Equal(t, w.fullConv, own["mauriceReportingConversation"]["id"])
}

func TestReportingDeletedConversationsNeedIncludeDeleted(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedReportingWorld(seed)

	hidden := gqlData[conversationsData](t, postGraphQL(t, server, adminHeaders(), conversationsQuery,
		map[string]any{"filter": map[string]any{"instance": "maurice-crapettes"}}))
	require.Len(t, hidden.Page.Items, 1)
	assert.Equal(t, w.publicConv, hidden.Page.Items[0].ID)

	shown := gqlData[conversationsData](t, postGraphQL(t, server, adminHeaders(), conversationsQuery,
		map[string]any{"filter": map[string]any{"instance": "maurice-crapettes", "includeDeleted": true}, "limit": 1}))
	require.Len(t, shown.Page.Items, 1)
	assert.Equal(t, w.deleted, shown.Page.Items[0].ID, "newest first")
	assert.Equal(t, "2026-11-10T16:00:00Z", *shown.Page.Items[0].DeletedAt)
	assert.Nil(t, shown.Page.Items[0].Status, "a conversation without turns has no status")
	assert.Nil(t, shown.Page.Items[0].LastTurnStartedAt)
	assert.Zero(t, shown.Page.Items[0].Turns.Total)
	require.NotNil(t, shown.Page.NextCursor)

	next := gqlData[conversationsData](t, postGraphQL(t, server, adminHeaders(), conversationsQuery,
		map[string]any{"filter": map[string]any{"instance": "maurice-crapettes", "includeDeleted": true}, "limit": 1,
			"cursor": *shown.Page.NextCursor}))
	require.Len(t, next.Page.Items, 1)
	assert.Equal(t, w.publicConv, next.Page.Items[0].ID)
	require.NotNil(t, next.Page.Items[0].Status)
	assert.Equal(t, "CANCELLED", *next.Page.Items[0].Status)
	require.NotNil(t, next.Page.Items[0].LastTurnStartedAt)
	assert.Equal(t, "2026-11-10T15:00:00Z", *next.Page.Items[0].LastTurnStartedAt)
	assert.Equal(t, 1, next.Page.Items[0].Turns.Total)
	assert.Nil(t, next.Page.NextCursor)
}

func TestReportingRejectsInvalidPagingAndFilters(t *testing.T) {
	t.Parallel()
	server, _, _ := newReportingDB(t)
	tests := []struct {
		name      string
		variables map[string]any
		want      string
	}{
		{"zero limit", map[string]any{"limit": 0}, "invalid argument: limit 0 is outside 1..200"},
		{"limit above maximum", map[string]any{"limit": 201}, "invalid argument: limit 201 is outside 1..200"},
		{"invalid cursor", map[string]any{"cursor": "bogus"}, "invalid cursor"},
		{"bad date", map[string]any{"from": "2026-13-01"}, `invalid argument: from "2026-13-01" is not a YYYY-MM-DD date`},
		{"bad user", map[string]any{"userId": "eric"}, "invalid argument: userId is not a UUID"},
	}
	for _, tt := range tests {
		filter := map[string]any{"instance": "maurice"}
		variables := map[string]any{"filter": filter}
		for key, value := range tt.variables {
			if key == "limit" || key == "cursor" {
				variables[key] = value
			} else {
				filter[key] = value
			}
		}
		resp := postGraphQL(t, server, adminHeaders(), conversationsQuery, variables)
		require.Len(t, resp.Errors, 1, tt.name)
		assert.Equal(t, tt.want, resp.Errors[0].Message, tt.name)
	}
}

// The reporting fields leave the owner scope untouched: each user still sees
// only their own undeleted conversations, on their own instance.
func TestReportingLeavesOwnerIsolationUnchanged(t *testing.T) {
	t.Parallel()
	_, seed, db := newReportingDB(t)
	w := seedReportingWorld(seed)
	ctx := context.Background()

	list, err := db.ListConversations(ctx, w.publicUser, mauricereport.MaxLimit)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, w.publicConv, list[0].ID)

	_, err = db.GetConversation(ctx, w.publicUser, w.fullConv)
	require.ErrorIs(t, err, maurice.ErrConversationNotFound, "the same identity's conversation on the other instance")
	_, err = db.GetConversation(ctx, w.otherUser, w.publicConv)
	require.ErrorIs(t, err, maurice.ErrConversationNotFound, "another user of the same instance")
}
