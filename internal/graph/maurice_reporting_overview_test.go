package graph

import (
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/sperano/puckdb/internal/mauricereport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const usageFields = `calls failedCalls
	inputTokens { total reportedCalls unreportedCalls complete }
	outputTokens { total reportedCalls unreportedCalls complete }
	cacheCreationInputTokens { total reportedCalls unreportedCalls complete }
	cacheReadInputTokens { total reportedCalls unreportedCalls complete }
	callLatency { count meanSeconds p50Seconds p95Seconds maxSeconds }`

const activityFields = `conversations
	turns { total running succeeded failed cancelled terminal succeededRate failedRate cancelledRate }
	firstTurnStartedAt lastTurnStartedAt
	turnLatency { count meanSeconds p50Seconds p95Seconds maxSeconds }
	usage { ` + usageFields + ` }
	titleGeneration { ` + usageFields + ` }
	processingSeconds`

const overviewQuery = `query($filter: MauriceReportingConversationFilter!) {
	mauriceReportingOverview(filter: $filter) {
		userCount activeUserCount
		activity { ` + activityFields + ` }
		models { provider model turns usage { calls inputTokens { total } } }
	}
}`

const userActivityQuery = `query($filter: MauriceReportingConversationFilter!, $limit: Int, $cursor: String) {
	mauriceReportingUserActivity(filter: $filter, limit: $limit, cursor: $cursor) {
		items { user { id instance } active activity { conversations turns { total } usage { calls } } }
		nextCursor
	}
}`

// seedOverview adds to the public user's conversation of reportingWorld
// (a cancelled turn at reportingT0 whose sonnet round reports no count): a
// succeeded 4s turn a minute later with a sonnet round of 100 input and 10
// output tokens, then a title call reporting 5 input tokens only.
func seedOverview(seed *mauricefixtures.Seeder) reportingWorld {
	w := seedReportingWorld(seed)
	started := reportingT0.Add(time.Minute)
	turn := seed.Turn(mauricefixtures.Turn{ConversationID: w.publicConv, UserID: w.publicUser, Number: 1,
		Status: mauricefixtures.StatusSucceeded, StartedAt: started, Duration: 4 * time.Second})
	seed.Call(mauricefixtures.Call{ConversationID: w.publicConv, TurnID: turn, Provider: "anthropic", Model: "claude-sonnet-5-5",
		StartedAt: started, InputTokens: mauricefixtures.Tokens(100), OutputTokens: mauricefixtures.Tokens(10)})
	seed.Call(mauricefixtures.Call{ConversationID: w.publicConv, Provider: "anthropic", Model: "claude-sonnet-5-5",
		StartedAt: reportingT0.Add(2 * time.Minute), InputTokens: mauricefixtures.Tokens(5)})
	return w
}

// Unreported counts are null totals or incomplete sums, never zeros; rates
// are over the finished turns; the title call is in usage and on its own.
func TestReportingOverviewServesExactAggregates(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	seedOverview(seed)

	resp := postGraphQL(t, server, adminHeaders(), overviewQuery,
		map[string]any{"filter": map[string]any{"instance": "maurice-crapettes"}})
	require.Empty(t, resp.Errors)
	assert.JSONEq(t, `{"mauriceReportingOverview": {
		"userCount": 2, "activeUserCount": 1,
		"activity": {
			"conversations": 1,
			"turns": {"total": 2, "running": 0, "succeeded": 1, "failed": 0, "cancelled": 1, "terminal": 2,
				"succeededRate": 0.5, "failedRate": 0, "cancelledRate": 0.5},
			"firstTurnStartedAt": "2026-11-10T15:00:00Z", "lastTurnStartedAt": "2026-11-10T15:01:00Z",
			"turnLatency": {"count": 1, "meanSeconds": 4, "p50Seconds": 4, "p95Seconds": 4, "maxSeconds": 4},
			"usage": {"calls": 3, "failedCalls": 0,
				"inputTokens": {"total": 105, "reportedCalls": 2, "unreportedCalls": 1, "complete": false},
				"outputTokens": {"total": 10, "reportedCalls": 1, "unreportedCalls": 2, "complete": false},
				"cacheCreationInputTokens": {"total": null, "reportedCalls": 0, "unreportedCalls": 3, "complete": false},
				"cacheReadInputTokens": {"total": null, "reportedCalls": 0, "unreportedCalls": 3, "complete": false},
				"callLatency": {"count": 3, "meanSeconds": 1, "p50Seconds": 1, "p95Seconds": 1, "maxSeconds": 1}},
			"titleGeneration": {"calls": 1, "failedCalls": 0,
				"inputTokens": {"total": 5, "reportedCalls": 1, "unreportedCalls": 0, "complete": true},
				"outputTokens": {"total": null, "reportedCalls": 0, "unreportedCalls": 1, "complete": false},
				"cacheCreationInputTokens": {"total": null, "reportedCalls": 0, "unreportedCalls": 1, "complete": false},
				"cacheReadInputTokens": {"total": null, "reportedCalls": 0, "unreportedCalls": 1, "complete": false},
				"callLatency": {"count": 1, "meanSeconds": 1, "p50Seconds": 1, "p95Seconds": 1, "maxSeconds": 1}},
			"processingSeconds": 6
		},
		"models": [{"provider": "anthropic", "model": "claude-sonnet-5-5", "turns": 2,
			"usage": {"calls": 3, "inputTokens": {"total": 105}}}]
	}}`, string(resp.Data))
}

// No row selected is measured zero: known zero totals, no rate, no latency.
func TestReportingOverviewOfNothing(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	seedOverview(seed)

	resp := postGraphQL(t, server, adminHeaders(), `query($filter: MauriceReportingConversationFilter!) {
		mauriceReportingOverview(filter: $filter) {
			userCount activeUserCount
			activity { conversations turns { total terminal succeededRate } firstTurnStartedAt
				turnLatency { count meanSeconds } usage { calls inputTokens { total complete } } processingSeconds }
			models { model }
		}
	}`, map[string]any{"filter": map[string]any{"instance": "maurice-crapettes", "from": "2026-11-11"}})
	require.Empty(t, resp.Errors)
	assert.JSONEq(t, `{"mauriceReportingOverview": {
		"userCount": 2, "activeUserCount": 0,
		"activity": {"conversations": 0, "turns": {"total": 0, "terminal": 0, "succeededRate": null},
			"firstTurnStartedAt": null, "turnLatency": {"count": 0, "meanSeconds": null},
			"usage": {"calls": 0, "inputTokens": {"total": 0, "complete": true}}, "processingSeconds": 0},
		"models": []
	}}`, string(resp.Data))
}

type userActivityData struct {
	Page struct {
		Items []struct {
			User struct {
				ID       string `json:"id"`
				Instance string `json:"instance"`
			} `json:"user"`
			Active   bool `json:"active"`
			Activity struct {
				Conversations int `json:"conversations"`
				Turns         struct {
					Total int `json:"total"`
				} `json:"turns"`
				Usage struct {
					Calls int `json:"calls"`
				} `json:"usage"`
			} `json:"activity"`
		} `json:"items"`
		NextCursor *string `json:"nextCursor"`
	} `json:"mauriceReportingUserActivity"`
}

func TestReportingUserActivityPagesTheInstancesUsers(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedOverview(seed)
	filter := map[string]any{"instance": "maurice-crapettes"}

	first := gqlData[userActivityData](t, postGraphQL(t, server, adminHeaders(), userActivityQuery,
		map[string]any{"filter": filter, "limit": 1}))
	require.Len(t, first.Page.Items, 1)
	assert.Equal(t, w.otherUser, first.Page.Items[0].User.ID, "newest first, listed although inactive")
	assert.False(t, first.Page.Items[0].Active)
	assert.Zero(t, first.Page.Items[0].Activity.Turns.Total)
	require.NotNil(t, first.Page.NextCursor)

	second := gqlData[userActivityData](t, postGraphQL(t, server, adminHeaders(), userActivityQuery,
		map[string]any{"filter": filter, "limit": 1, "cursor": *first.Page.NextCursor}))
	require.Len(t, second.Page.Items, 1)
	got := second.Page.Items[0]
	assert.Equal(t, w.publicUser, got.User.ID)
	assert.Equal(t, "maurice-crapettes", got.User.Instance)
	assert.True(t, got.Active)
	assert.Equal(t, 1, got.Activity.Conversations, "the deleted conversation is left out")
	assert.Equal(t, 2, got.Activity.Turns.Total)
	assert.Equal(t, 3, got.Activity.Usage.Calls)
	assert.Nil(t, second.Page.NextCursor)
}

func TestReportingOverviewRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedOverview(seed)
	conversations := gqlData[conversationsData](t, postGraphQL(t, server, adminHeaders(), conversationsQuery,
		map[string]any{"filter": map[string]any{"instance": "maurice-crapettes", "includeDeleted": true}, "limit": 1}))
	require.NotNil(t, conversations.Page.NextCursor)
	tests := []struct {
		name      string
		query     string
		variables map[string]any
		want      string
	}{
		{"user activity: zero limit", userActivityQuery, map[string]any{"limit": 0}, "invalid argument: limit 0 is outside 1..200"},
		{"user activity: limit above maximum", userActivityQuery, map[string]any{"limit": 201}, "invalid argument: limit 201 is outside 1..200"},
		{"user activity: malformed cursor", userActivityQuery, map[string]any{"cursor": "bogus"}, "invalid cursor"},
		{"user activity: the conversation list's cursor", userActivityQuery,
			map[string]any{"cursor": *conversations.Page.NextCursor}, "invalid cursor"},
		{"user activity: another instance's user", userActivityQuery,
			map[string]any{"filter": map[string]any{"instance": "maurice-crapettes", "userId": w.fullUser}},
			mauricereport.ErrUserNotInInstance.Error()},
		{"overview: another instance's user", overviewQuery,
			map[string]any{"filter": map[string]any{"instance": "maurice-crapettes", "userId": w.fullUser}},
			mauricereport.ErrUserNotInInstance.Error()},
		{"overview: to before from", overviewQuery,
			map[string]any{"filter": map[string]any{"instance": "maurice", "from": "2026-11-02", "to": "2026-11-01"}},
			"invalid argument: to 2026-11-01 is before from 2026-11-02"},
		{"overview: search too long", overviewQuery,
			map[string]any{"filter": map[string]any{"instance": "maurice", "search": strings.Repeat("a", mauricereport.MaxSearchRunes+1)}},
			"invalid argument: search is 201 characters, at most 200"},
		{"overview: missing instance", overviewQuery,
			map[string]any{"filter": map[string]any{"instance": ""}}, "invalid argument: instance is required"},
	}
	for _, tt := range tests {
		variables := tt.variables
		if _, ok := variables["filter"]; !ok {
			variables["filter"] = map[string]any{"instance": "maurice-crapettes", "includeDeleted": true}
		}
		resp := postGraphQL(t, server, adminHeaders(), tt.query, variables)
		require.Len(t, resp.Errors, 1, tt.name)
		assert.Equal(t, tt.want, resp.Errors[0].Message, tt.name)
		assert.JSONEq(t, "null", string(resp.Data), tt.name)
	}
}
