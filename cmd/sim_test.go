package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sperano/puckdb/graph/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----------------------------------------------------------------------------
// parsePoolID
// ----------------------------------------------------------------------------

func TestParsePoolID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		input     string
		expected  int
		expectErr bool
	}{
		{"valid", "1", 1, false},
		{"large", "12345", 12345, false},
		{"non-numeric", "abc", 0, true},
		{"empty", "", 0, true},
		{"zero rejected", "0", 0, true},
		{"negative rejected", "-5", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePoolID(tc.input)
			if tc.expectErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// ----------------------------------------------------------------------------
// parseCreateSimPoolInput — the YAML config loader
// ----------------------------------------------------------------------------

func TestParseCreateSimPoolInput_Valid(t *testing.T) {
	t.Parallel()
	body := `
name: Test
season: 20242025
categories: [G, A]
rosterPositions:
  - {slot: C, count: 2}
  - {slot: BN, count: 5}
waiverDays: 2
draftRounds: 18
maxLlmCostUsdPerPool: 200.0
agents:
  - provider: anthropic
    model: claude-haiku-4-5
    strategy: balanced
`
	in, err := parseCreateSimPoolInput(strings.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, "Test", in.Name)
	assert.Equal(t, 20242025, in.Season)
	require.Len(t, in.RosterPositions, 2)
	assert.Equal(t, "C", in.RosterPositions[0].Slot)
	assert.Equal(t, 5, in.RosterPositions[1].Count)
	require.Len(t, in.Agents, 1)
	assert.Equal(t, "anthropic", in.Agents[0].Provider)
}

// Strict decoding — a typo in the YAML should surface immediately,
// not silently get dropped.
func TestParseCreateSimPoolInput_RejectsUnknownFields(t *testing.T) {
	t.Parallel()
	body := `
name: Test
typo_field: 42
season: 20242025
categories: []
rosterPositions: []
waiverDays: 1
draftRounds: 1
maxLlmCostUsdPerPool: 0
agents: []
`
	_, err := parseCreateSimPoolInput(strings.NewReader(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "typo_field")
}

func TestParseCreateSimPoolInput_RejectsMalformedYAML(t *testing.T) {
	t.Parallel()
	// Unclosed flow-style bracket — ill-formed under any YAML parser.
	_, err := parseCreateSimPoolInput(strings.NewReader("name: [unterminated\n"))
	require.Error(t, err)
}

// loadCreateSimPoolInputFromFile end-to-end: write a file, read it back.
func TestLoadCreateSimPoolInputFromFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
name: FromFile
season: 20242025
categories: [G]
rosterPositions: []
waiverDays: 1
draftRounds: 1
maxLlmCostUsdPerPool: 100
agents: []
`)
	require.NoError(t, os.WriteFile(path, body, 0o644))

	in, err := loadCreateSimPoolInputFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "FromFile", in.Name)
}

func TestLoadCreateSimPoolInputFromFile_MissingPath(t *testing.T) {
	t.Parallel()
	_, err := loadCreateSimPoolInputFromFile("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--config")
}

func TestLoadCreateSimPoolInputFromFile_NoSuchFile(t *testing.T) {
	t.Parallel()
	_, err := loadCreateSimPoolInputFromFile("/nonexistent/path.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open")
}

// ----------------------------------------------------------------------------
// asciiBar / repeatRune
// ----------------------------------------------------------------------------

func TestAsciiBar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		current, total, width int
		expected              string
	}{
		{0, 10, 10, "[          ]"},
		{5, 10, 10, "[#####     ]"},
		{10, 10, 10, "[##########]"},
		{0, 0, 5, "[     ]"},         // total=0 → empty bar (not panic)
		{15, 10, 10, "[##########]"}, // overflow clamps to width
		{-1, 10, 10, "[          ]"},
	}
	for _, tc := range cases {
		got := asciiBar(tc.current, tc.total, tc.width)
		assert.Equal(t, tc.expected, got, "current=%d total=%d width=%d", tc.current, tc.total, tc.width)
	}
}

func TestRepeatRune(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", repeatRune('#', 0))
	assert.Equal(t, "", repeatRune('#', -1))
	assert.Equal(t, "###", repeatRune('#', 3))
}

// ----------------------------------------------------------------------------
// renderSimPoolStatus
// ----------------------------------------------------------------------------

func TestRenderSimPoolStatus_NotFound(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderSimPoolStatus(&buf, &SimPoolStatusResult{Pool: nil})
	assert.Contains(t, buf.String(), "not found")
}

func TestRenderSimPoolStatus_NilResult(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderSimPoolStatus(&buf, nil)
	assert.Contains(t, buf.String(), "not found")
}

func TestRenderSimPoolStatus_FullSummary(t *testing.T) {
	t.Parallel()
	simDate := "2024-11-15"
	pool := &model.SimPool{
		ID: 7, Name: "Test Pool", Season: 20242025, Status: "running",
		SimDate: &simDate, TotalLlmCostUsd: 12.34,
		Agents: []*model.SimAgent{
			{ID: 1, TeamName: "Sonnet", TotalRotoPoints: 47.0},
			{ID: 2, TeamName: "Haiku", TotalRotoPoints: 52.5},
		},
	}
	progress := &model.ProgressReport{
		Total: 200, Completed: 100,
		Groups: []*model.ProgressGroup{
			{Header: "Draft", StartedAt: 1, Bars: []*model.ProgressBar{{Current: 90, Total: 90}}},
			{Header: "Season", StartedAt: 2, Bars: []*model.ProgressBar{{Current: 10, Total: 110}}},
		},
	}

	var buf bytes.Buffer
	renderSimPoolStatus(&buf, &SimPoolStatusResult{Pool: pool, Progress: progress})
	out := buf.String()

	// Summary block — labels followed by colon, values aligned.
	assert.Contains(t, out, "Pool 7:")
	assert.Contains(t, out, `"Test Pool"`)
	assert.Contains(t, out, "season:   20242025")
	assert.Contains(t, out, "status:   running")
	assert.Contains(t, out, "sim_date: 2024-11-15")
	assert.Contains(t, out, "llm_cost: $12.34")

	// Progress: Draft is complete (100%), Season has started.
	assert.Contains(t, out, "Draft")
	assert.Contains(t, out, "Season")

	// Standings: Haiku has higher roto points so should rank #1.
	haikuIdx := strings.Index(out, "Haiku")
	sonnetIdx := strings.Index(out, "Sonnet")
	require.Greater(t, haikuIdx, 0)
	require.Greater(t, sonnetIdx, 0)
	assert.Less(t, haikuIdx, sonnetIdx, "Haiku (52.5 points) must print before Sonnet (47.0)")
}

func TestRenderSimPoolStatus_NullSimDate(t *testing.T) {
	t.Parallel()
	pool := &model.SimPool{ID: 1, Season: 20242025, Status: "draft"}
	var buf bytes.Buffer
	renderSimPoolStatus(&buf, &SimPoolStatusResult{Pool: pool})
	assert.Contains(t, buf.String(), "sim_date: (not started)",
		"null SimDate renders as '(not started)' rather than '<nil>'")
}

func TestRenderSimPoolStatus_HidesUnstartedGroups(t *testing.T) {
	t.Parallel()
	pool := &model.SimPool{ID: 1, Season: 1, Status: "draft"}
	progress := &model.ProgressReport{
		Groups: []*model.ProgressGroup{
			{Header: "Draft", StartedAt: 1, Bars: []*model.ProgressBar{{Current: 5, Total: 10}}},
			{Header: "Season", StartedAt: 0}, // not yet started
		},
	}
	var buf bytes.Buffer
	renderSimPoolStatus(&buf, &SimPoolStatusResult{Pool: pool, Progress: progress})
	out := buf.String()
	assert.Contains(t, out, "Draft")
	assert.NotContains(t, out, "Season",
		"unstarted groups must not render — the user shouldn't see Phase 2 progress before draft completes")
}

func TestRenderSimPoolStatus_StandingsTieBreaker(t *testing.T) {
	t.Parallel()
	pool := &model.SimPool{
		ID: 1, Season: 1, Status: "x",
		Agents: []*model.SimAgent{
			{ID: 1, TeamName: "Alpha", TotalRotoPoints: 10.0},
			{ID: 2, TeamName: "Beta", TotalRotoPoints: 10.0},
		},
	}
	var buf bytes.Buffer
	renderSimPoolStatus(&buf, &SimPoolStatusResult{Pool: pool})
	out := buf.String()
	// Tied at 10.0 → name-ascending tiebreak so Alpha lands #1.
	alphaIdx := strings.Index(out, "Alpha")
	betaIdx := strings.Index(out, "Beta")
	assert.Less(t, alphaIdx, betaIdx)
}

// ----------------------------------------------------------------------------
// GraphQLClient sim methods — round-trip via httptest
// ----------------------------------------------------------------------------

// fakeGraphQLServer returns an httptest.Server that decodes the
// request and returns the provided response body. responseBody is
// inlined as the JSON top-level "data" payload.
func fakeGraphQLServer(t *testing.T, expectedField string, responseBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		assert.Contains(t, req.Query, expectedField,
			"request body must reference the expected mutation/query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":` + responseBody + `}`))
	}))
}

// newClientForTestServer builds a GraphQLClient pointed at an
// httptest.Server's URL directly, bypassing the production
// "/graphql/query" suffix appended by NewGraphQLClient (the test
// server handles all paths the same way).
func newClientForTestServer(srv *httptest.Server) *GraphQLClient {
	c := NewGraphQLClient("http://placeholder")
	c.endpoint = srv.URL
	return c
}

func TestGraphQLClient_CancelSimPool(t *testing.T) {
	t.Parallel()
	srv := fakeGraphQLServer(t, "cancelSimPool",
		`{"cancelSimPool": {"id": 7, "name": "TestPool", "status": "cancelled", "totalLlmCostUsd": 1.5}}`)
	defer srv.Close()

	client := newClientForTestServer(srv)
	pool, err := client.CancelSimPool(context.Background(), 7)
	require.NoError(t, err)
	require.NotNil(t, pool)
	assert.Equal(t, 7, pool.ID)
	assert.Equal(t, "cancelled", pool.Status)
}

func TestGraphQLClient_CreateSimPool(t *testing.T) {
	t.Parallel()
	srv := fakeGraphQLServer(t, "createSimPool",
		`{"createSimPool": {"id": 42, "name": "Created", "status": "draft", "season": 20242025, "totalLlmCostUsd": 0, "agents": []}}`)
	defer srv.Close()
	client := newClientForTestServer(srv)

	in := &model.CreateSimPoolInput{Season: 20242025}
	pool, err := client.CreateSimPool(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, 42, pool.ID)
	assert.Equal(t, "Created", pool.Name)
}

// Server returning a GraphQL error (non-empty errors[]) → client
// surfaces the message rather than silently treating data:null as a
// success.
func TestGraphQLClient_SignalError_PropagatesGraphQLError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": null, "errors": [{"message": "workflow not running"}]}`))
	}))
	defer srv.Close()
	client := newClientForTestServer(srv)

	_, err := client.CancelSimPool(context.Background(), 99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow not running")
}

// Null payload → client errors out instead of returning a zero-valued
// SimPool.
func TestGraphQLClient_NullPayload_Errors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": {"cancelSimPool": null}}`))
	}))
	defer srv.Close()
	client := newClientForTestServer(srv)

	_, err := client.CancelSimPool(context.Background(), 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no data")
}

// SimPoolStatus round-trip — the read-side path the status command uses.
func TestGraphQLClient_SimPoolStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": {
			"pool": {"id": 7, "name": "Test", "season": 20242025, "status": "running", "totalLlmCostUsd": 0, "agents": [], "standings": []},
			"progress": {"total": 100, "completed": 50, "groups": []}
		}}`))
	}))
	defer srv.Close()
	client := newClientForTestServer(srv)

	res, err := client.SimPoolStatus(context.Background(), 7)
	require.NoError(t, err)
	require.NotNil(t, res.Pool)
	assert.Equal(t, 7, res.Pool.ID)
	require.NotNil(t, res.Progress)
	assert.Equal(t, 100, res.Progress.Total)
}
