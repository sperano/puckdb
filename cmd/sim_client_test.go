package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingGraphQLServer answers every request with `{"data": data}` (or
// with errors when errMsg is set). The returned func gives the last request;
// the handler goroutine stores it under a mutex.
func recordingGraphQLServer(t *testing.T, data, errMsg string) (*httptest.Server, func() graphQLRequest) {
	t.Helper()
	var (
		mu   sync.Mutex
		last graphQLRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			// require would call FailNow off the test goroutine.
			t.Errorf("decode GraphQL request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		last = req
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if errMsg != "" {
			body, err := json.Marshal(map[string]any{"data": nil, "errors": []map[string]string{{"message": errMsg}}})
			if err != nil {
				t.Errorf("encode GraphQL errors: %v", err)
			}
			_, _ = w.Write(body)
			return
		}
		_, _ = w.Write([]byte(`{"data":` + data + `}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() graphQLRequest {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

const simTurnFeedResponse = `{"simTurnFeed": {"lastId": 42, "hasMore": true, "turns": [
	{"id": 41, "poolId": 3, "agentId": 7, "teamName": "Stars", "strategySummary": "balanced",
	 "model": "claude", "phase": "team_name", "simDate": null, "status": "ok", "errorKind": null,
	 "rounds": 1, "costUsd": 0.0125, "completedAt": "2024-11-15T19:32:01.5Z", "toolCallCount": 0, "toolCalls": []},
	{"id": 42, "poolId": 3, "agentId": 7, "teamName": "Stars", "strategySummary": "balanced",
	 "model": "claude", "phase": "daily", "simDate": "2024-11-15", "status": "errored", "errorKind": "timeout",
	 "rounds": 2, "costUsd": 0.5, "completedAt": "2024-11-15T19:33:00Z", "toolCallCount": 40, "toolCalls": [
		{"roundIndex": 1, "sequence": 2, "toolName": "addPlayer", "recoveredName": "add_player",
		 "arguments": "{\"player_id\":1}", "outcome": "validation_rejected", "failureReason": "roster_full",
		 "appliedTransactionId": 99, "playerNames": ["Connor McDavid"]}]}]}}`

// SimTurnFeed sends its arguments as GraphQL variables and maps the
// response field by field onto the structs the formatters print.
func TestGraphQLClient_SimTurnFeed(t *testing.T) {
	t.Parallel()
	srv, lastRequest := recordingGraphQLServer(t, simTurnFeedResponse, "")
	pool, after := 3, 40
	page, err := newClientForTestServer(srv).SimTurnFeed(context.Background(), &pool, &after, 100)
	require.NoError(t, err)

	assert.Contains(t, lastRequest().Query, "simTurnFeed(")
	assert.Equal(t, map[string]any{"poolId": 3.0, "afterId": 40.0, "limit": 100.0}, lastRequest().Variables)
	assert.Equal(t, 42, page.LastID)
	assert.True(t, page.HasMore)
	require.Len(t, page.Entries, 2)

	first := page.Entries[0]
	assert.Nil(t, first.Turn.SimDate)
	assert.Nil(t, first.Turn.ErrorKind)
	assert.Empty(t, first.Calls)
	assert.True(t, first.Turn.CompletedAt.Equal(time.Date(2024, 11, 15, 19, 32, 1, 500_000_000, time.UTC)))

	second := page.Entries[1]
	tx, recovered, reason, kind := int64(99), "add_player", "roster_full", "timeout"
	date := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, tailTurn{
		ID: 42, PoolID: 3, AgentID: 7, TeamName: "Stars", Summary: "balanced", Model: "claude",
		Phase: "daily", SimDate: &date, Status: "errored", ErrorKind: &kind, Rounds: 2, CostUSD: 0.5,
		CompletedAt: second.Turn.CompletedAt, ToolCallCount: 40,
	}, second.Turn)
	assert.Equal(t, []tailToolCall{{
		RoundIndex: 1, Sequence: 2, ToolName: "addPlayer", RecoveredName: &recovered,
		Arguments: `{"player_id":1}`, Outcome: "validation_rejected", FailureReason: &reason,
		AppliedTxID: &tx, PlayerNames: []string{"Connor McDavid"},
	}}, second.Calls)
}

func TestGraphQLClient_SimTurnFeedAllPoolsSendsNulls(t *testing.T) {
	t.Parallel()
	srv, lastRequest := recordingGraphQLServer(t, `{"simTurnFeed": {"lastId": 0, "hasMore": false, "turns": []}}`, "")
	page, err := newClientForTestServer(srv).SimTurnFeed(context.Background(), nil, nil, tailBacklog)
	require.NoError(t, err)
	assert.Empty(t, page.Entries)
	assert.Equal(t, map[string]any{"poolId": nil, "afterId": nil, "limit": float64(tailBacklog)}, lastRequest().Variables)
}

// olderServerStatus is what gqlgen answers a query that fails validation
// with, such as a field an older server does not have.
const olderServerStatus = http.StatusUnprocessableEntity

// An API server without the field gets a message naming the fix. The
// fake answers like gqlgen: 422 with a GraphQL error body.
func TestGraphQLClient_SimFeedsOnOlderServer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field, command string
		call           func(*GraphQLClient) error
	}{
		{"simTurnFeed", "sim tail", func(c *GraphQLClient) error {
			_, err := c.SimTurnFeed(context.Background(), nil, nil, tailBacklog)
			return err
		}},
		{"simDraftPicks", "sim draft", func(c *GraphQLClient) error {
			_, err := c.SimDraftPicks(context.Background(), 1)
			return err
		}},
	}
	for _, tc := range cases {
		body, err := json.Marshal(map[string]any{"data": nil, "errors": []map[string]string{
			{"message": `Cannot query field "` + tc.field + `" on type "Query".`}}})
		require.NoError(t, err)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(olderServerStatus)
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		err = tc.call(newClientForTestServer(srv))
		require.ErrorContains(t, err, "predates "+tc.command+" over GraphQL; use a matching puckdb binary")
		assert.ErrorContains(t, err, tc.field)
	}
}

// The real handler's answer to an unknown field is what simFieldError
// recognizes, whatever status and encoding gqlgen uses for it.
func TestSimFieldError_RealHandlerUnknownField(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(graphqlHandler(&graph.Resolver{}, graph.AdminAuth{}))
	t.Cleanup(srv.Close)
	c := newClientForTestServer(srv)
	const field = "simFieldThisServerLacks"
	_, err := c.execute(context.Background(), "{ "+field+" }", nil)
	require.Error(t, err)
	err = c.simFieldError(err, field, "sim tail")
	assert.ErrorContains(t, err, "predates sim tail over GraphQL")
}

func TestGraphQLClient_SimTurnFeedOtherErrorsPassThrough(t *testing.T) {
	t.Parallel()
	srv, _ := recordingGraphQLServer(t, "", "limit must be between 1 and 100, got 101")
	_, err := newClientForTestServer(srv).SimTurnFeed(context.Background(), nil, nil, 101)
	require.EqualError(t, err, "graphql error: limit must be between 1 and 100, got 101")
}

func TestGraphQLClient_SimDraftPicks(t *testing.T) {
	t.Parallel()
	srv, lastRequest := recordingGraphQLServer(t, `{"simDraftPicks": [
		{"agentId": 4, "teamName": "", "strategySummary": "", "model": "m", "draftPosition": 2,
		 "round": 1, "pick": 3, "playerId": 8478402, "playerName": "Connor McDavid", "position": "C", "reasoning": "r"},
		{"agentId": 5, "teamName": "T", "strategySummary": "s", "model": "", "draftPosition": null,
		 "round": 2, "pick": 1, "playerId": 0, "playerName": "", "position": "?", "reasoning": ""}]}`, "")
	rows, err := newClientForTestServer(srv).SimDraftPicks(context.Background(), 9)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"poolId": 9.0}, lastRequest().Variables)
	pos := int32(2)
	assert.Equal(t, []draftRow{
		{AgentID: 4, Model: "m", DraftPosition: &pos, Round: 1, Pick: 3, PlayerID: 8478402,
			PlayerName: "Connor McDavid", Position: "C", Reasoning: "r"},
		{AgentID: 5, TeamName: "T", Summary: "s", Round: 2, Pick: 1, Position: "?"},
	}, rows)
}

func TestGraphQLClient_SimDraftPicksMissingPool(t *testing.T) {
	t.Parallel()
	srv, _ := recordingGraphQLServer(t, `{"simDraftPicks": null}`, "")
	_, err := newClientForTestServer(srv).SimDraftPicks(context.Background(), 9)
	assert.True(t, errors.Is(err, errSimPoolNotFound), "got %v", err)
}
