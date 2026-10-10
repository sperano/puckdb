package graph

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/graph/generated"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Contract tests of the draft ranking API: real schema, directives and
// resolvers over the shared fixture store, compared with the service page
// and the CSV export the CLI writes.

const draftTestAuthor = "eric"

var draftTestNow = draftfixtures.AsOf.Add(time.Hour)

func newDraftTestServer(t *testing.T) (*httptest.Server, *draftfixtures.Store, *draftrank.Service) {
	t.Helper()
	store := draftfixtures.NewStore()
	snapshot, err := draftfixtures.Snapshot(uuid.NewSHA1(uuid.NameSpaceOID, []byte("api")), draftfixtures.AsOf)
	require.NoError(t, err)
	store.Add(snapshot)
	svc := draftrank.NewService(store, store, draftrank.ServiceOptions{Now: func() time.Time { return draftTestNow }})
	schema := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers:  &Resolver{Draft: svc},
		Directives: generated.DirectiveRoot{Admin: NewAdminDirective(AdminAuth{Group: config.DefaultAdminGroup})},
	}))
	schema.AddTransport(transport.POST{})
	server := httptest.NewServer(httpx.HeaderContext(schema))
	t.Cleanup(server.Close)
	return server, store, svc
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func postGraphQL(t *testing.T, server *httptest.Server, headers http.Header, query string, variables map[string]any) gqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		req.Header[key] = values
	}
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var decoded gqlResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	return decoded
}

const draftRankingsQuery = `query($input: DraftRankingsInput!) { draftRankings(input: $input) {
	status scenario totalCount offset limit latestSnapshotId
	snapshot { id identity scenarios versions { scenario version } news { sourceId status } }
	issues { code message }
	rows { playerKey name overallRank positionRank baselineRank rankChange tier value adjustedValue officialScore
		eligiblePositions positionRanks { position rank }
		placements { scenario overallRank value }
		adjustment { reasons { eventId evidence { publisher url quote } } effects { scenario missedGames } } }
} }`

type apiRow struct {
	PlayerKey         string   `json:"playerKey"`
	OverallRank       int      `json:"overallRank"`
	PositionRank      int      `json:"positionRank"`
	BaselineRank      int      `json:"baselineRank"`
	RankChange        int      `json:"rankChange"`
	Tier              int      `json:"tier"`
	Value             float64  `json:"value"`
	AdjustedValue     float64  `json:"adjustedValue"`
	OfficialScore     float64  `json:"officialScore"`
	EligiblePositions []string `json:"eligiblePositions"`
	Placements        []struct {
		Scenario    string `json:"scenario"`
		OverallRank int    `json:"overallRank"`
	} `json:"placements"`
	Adjustment *struct {
		Reasons []struct {
			Evidence []struct {
				Publisher string `json:"publisher"`
			} `json:"evidence"`
		} `json:"reasons"`
		Effects []struct {
			Scenario    string  `json:"scenario"`
			MissedGames float64 `json:"missedGames"`
		} `json:"effects"`
	} `json:"adjustment"`
}

type apiPage struct {
	Status     string   `json:"status"`
	Scenario   string   `json:"scenario"`
	TotalCount int      `json:"totalCount"`
	Limit      int      `json:"limit"`
	Rows       []apiRow `json:"rows"`
	Snapshot   struct {
		ID        string   `json:"id"`
		Identity  string   `json:"identity"`
		Scenarios []string `json:"scenarios"`
	} `json:"snapshot"`
	Issues []struct {
		Code string `json:"code"`
	} `json:"issues"`
}

func fetchRankings(t *testing.T, server *httptest.Server, input map[string]any) apiPage {
	t.Helper()
	resp := postGraphQL(t, server, nil, draftRankingsQuery, map[string]any{"input": input})
	require.Empty(t, resp.Errors)
	var data struct {
		DraftRankings apiPage `json:"draftRankings"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	return data.DraftRankings
}

func TestDraftRankingsAPI_MatchesServiceAndCSVExport(t *testing.T) {
	server, _, svc := newDraftTestServer(t)
	input := map[string]any{"league": draftfixtures.LeagueKey, "positions": []string{"C", "LW"}, "sort": "VALUE", "limit": 3, "offset": 1}
	api := fetchRankings(t, server, input)

	page, err := svc.Rankings(t.Context(), draftrank.LeagueRef{LeagueKey: draftfixtures.LeagueKey}, uuid.Nil,
		draftrank.Query{Positions: []string{"C", "LW"}, Sort: draftrank.SortValue, Limit: 3, Offset: 1})
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, draftrank.WriteCSV(&buf, page))
	records, err := csv.NewReader(&buf).ReadAll()
	require.NoError(t, err)

	assert.Equal(t, "READY", api.Status)
	assert.Equal(t, "BASE", api.Scenario)
	assert.Equal(t, page.Total, api.TotalCount)
	assert.Equal(t, page.Snapshot.ID.String(), api.Snapshot.ID)
	assert.Equal(t, page.Snapshot.Identity, api.Snapshot.Identity)
	require.Len(t, api.Rows, len(page.Rows))
	require.Len(t, records, len(page.Rows)+1)
	header := records[0]
	column := func(record []string, name string) string {
		for i, h := range header {
			if h == name {
				return record[i]
			}
		}
		t.Fatalf("no CSV column %s", name)
		return ""
	}
	for i, row := range page.Rows {
		got, record := api.Rows[i], records[i+1]
		assert.Equal(t, row.PlayerKey, got.PlayerKey)
		assert.Equal(t, row.PlayerKey, column(record, "player_key"))
		assert.Equal(t, row.Placement.OverallRank, got.OverallRank)
		assert.Equal(t, strconv.Itoa(got.OverallRank), column(record, "overall_rank"))
		assert.Equal(t, row.PositionRank, got.PositionRank)
		assert.Equal(t, row.Placement.Value, got.Value)
		assert.Equal(t, strconv.FormatFloat(got.Value, 'g', -1, 64), column(record, "value"))
		assert.Equal(t, row.Placement.AdjustedValue, got.AdjustedValue)
		assert.Equal(t, strconv.FormatFloat(got.AdjustedValue, 'g', -1, 64), column(record, "adjusted_value"))
		assert.Equal(t, row.RankChange, got.RankChange)
		assert.Len(t, got.Placements, len(draftrank.Scenarios))
	}
}

func TestDraftRankingsAPI_PinnedSnapshotPagesAreConsistent(t *testing.T) {
	server, _, _ := newDraftTestServer(t)
	first := fetchRankings(t, server, map[string]any{"league": draftfixtures.LeagueKey, "limit": 4})
	full := fetchRankings(t, server, map[string]any{"league": "1001", "season": draftfixtures.Season, "limit": 100})
	second := fetchRankings(t, server, map[string]any{"league": draftfixtures.LeagueKey, "snapshotId": first.Snapshot.ID, "limit": 4, "offset": 4})

	var keys []string
	for _, row := range append(first.Rows, second.Rows...) {
		keys = append(keys, row.PlayerKey)
	}
	var fullKeys []string
	for _, row := range full.Rows {
		fullKeys = append(fullKeys, row.PlayerKey)
	}
	assert.Equal(t, fullKeys, keys, "a league key and its numeric ID resolve to the same pages")
	assert.Equal(t, first.Snapshot.ID, second.Snapshot.ID)
	assert.Equal(t, maxDraftPageSize, fetchRankings(t, server, map[string]any{"league": draftfixtures.LeagueKey, "limit": 5000}).Limit)
	assert.Equal(t, defaultDraftPageSize, fetchRankings(t, server, map[string]any{"league": draftfixtures.LeagueKey}).Limit)
}

func TestDraftPlayerComparisonAPI_ExplainsNewsWithEvidence(t *testing.T) {
	server, _, _ := newDraftTestServer(t)
	resp := postGraphQL(t, server, nil, `query($input: DraftComparisonInput!) { draftPlayerComparison(input: $input) {
		rows { playerKey rankChange placements { scenario overallRank } adjustment { reasons { eventId evidence { publisher } } effects { scenario missedGames } } } } }`,
		map[string]any{"input": map[string]any{"league": draftfixtures.LeagueKey, "playerKeys": []string{draftfixtures.SuspendedKey, draftfixtures.CenterWing}}})
	require.Empty(t, resp.Errors)
	var data struct {
		DraftPlayerComparison apiPage `json:"draftPlayerComparison"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	rows := data.DraftPlayerComparison.Rows
	require.Len(t, rows, 2)
	suspended := rows[0]
	if suspended.PlayerKey != draftfixtures.SuspendedKey {
		suspended = rows[1]
	}
	require.NotNil(t, suspended.Adjustment)
	assert.Equal(t, "NHL.com", suspended.Adjustment.Reasons[0].Evidence[0].Publisher)
	assert.Len(t, suspended.Adjustment.Effects, 3)
	assert.Negative(t, suspended.RankChange)
}

func TestDraftAPI_ExistingAuthenticationApplies(t *testing.T) {
	server, store, _ := newDraftTestServer(t)

	leagues := postGraphQL(t, server, nil, `{ draftLeagues(season: 2026) { league { leagueKey } status } }`, nil)
	assert.Empty(t, leagues.Errors, "draft reads follow the other data queries: authenticated by the proxy, not @admin")
	denied := postGraphQL(t, server, nil, `mutation { dropDatabase }`, nil)
	require.NotEmpty(t, denied.Errors, "admin mutations stay guarded on the same server")

	headers := http.Header{"X-Authentik-Username": []string{draftTestAuthor}}
	created := postGraphQL(t, server, headers, `mutation($input: DraftOverrideCreateInput!) { createDraftOverride(input: $input) { id createdBy state kind scenario } }`,
		map[string]any{"input": map[string]any{
			"playerKey": draftfixtures.SuspendedKey, "leagueKey": draftfixtures.LeagueKey, "kind": "MISSED_GAMES",
			"scenario": "BASE", "value": 10, "reason": "appeal reduced the suspension",
		}})
	require.Empty(t, created.Errors)
	require.Len(t, store.Overrides, 1)
	assert.Equal(t, draftTestAuthor, store.Overrides[0].CreatedBy, "the override author is the authenticated user")
	assert.Equal(t, draftTestNow, store.Overrides[0].CreatedAt)

	store.Changes = 1
	page := fetchRankings(t, server, map[string]any{"league": draftfixtures.LeagueKey, "limit": 1})
	var codes []string
	for _, issue := range page.Issues {
		codes = append(codes, issue.Code)
	}
	assert.Contains(t, codes, "OVERRIDES_CHANGED")

	reset := postGraphQL(t, server, nil, `mutation($id: String!) { resetDraftOverride(id: $id, reason: "appeal lost") }`,
		map[string]any{"id": store.Overrides[0].ID})
	require.Empty(t, reset.Errors)
	listed := postGraphQL(t, server, nil, `{ draftOverrides(includeInactive: true) { id state } }`, nil)
	require.Empty(t, listed.Errors)
	assert.Contains(t, string(listed.Data), `"state":"RESET"`)
}

func TestDraftAPI_ErrorsAndMissingDatabase(t *testing.T) {
	server, _, _ := newDraftTestServer(t)
	bad := postGraphQL(t, server, nil, draftRankingsQuery, map[string]any{"input": map[string]any{"league": draftfixtures.LeagueKey, "positions": []string{"UTIL"}}})
	require.NotEmpty(t, bad.Errors)
	assert.Contains(t, bad.Errors[0].Message, "unknown position")
	unknown := postGraphQL(t, server, nil, draftRankingsQuery, map[string]any{"input": map[string]any{"league": "465.l.1"}})
	require.NotEmpty(t, unknown.Errors)

	_, err := (&Resolver{}).draftRankings(t.Context(), model.DraftRankingsInput{League: draftfixtures.LeagueKey})
	assert.ErrorIs(t, err, errDraftNotConfigured)
}

func TestConfiguredLeagueIDs(t *testing.T) {
	t.Parallel()
	const season = 2026
	seasons := config.YahooSeasonsMap{season: {Leagues: []config.League{{LeagueID: 11}, {LeagueID: 22}}}}

	tests := []struct {
		name   string
		loader func() (config.YahooSeasonsMap, error)
		want   []int
	}{
		{name: "not configured", loader: nil, want: nil},
		{name: "loader error falls back to imported leagues", loader: func() (config.YahooSeasonsMap, error) {
			return nil, errors.New("can't read yahoo seasons config file")
		}, want: nil},
		{name: "season's leagues", loader: func() (config.YahooSeasonsMap, error) { return seasons, nil }, want: []int{11, 22}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, (&Resolver{YahooSeasons: tt.loader}).configuredLeagueIDs(season))
		})
	}
}
