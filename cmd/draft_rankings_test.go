package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const draftTestPenalty = 0.25

// ────────────────────────────────────────────────────────────────────────────
// draft rankings
// ────────────────────────────────────────────────────────────────────────────

func setDraftRankingDefaults() {
	viper.Reset()
	viper.Set(config.FlagDraftFormat, config.DefaultDraftFormat)
	viper.Set(config.FlagDraftSort, config.DefaultDraftSort)
}

func TestDraftRankingsRequestFromFlags(t *testing.T) {
	setDraftRankingDefaults()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagDraftLeague, draftfixtures.LeagueKey)
	viper.Set(config.FlagDraftPositions, " c, LW ,")
	viper.Set(config.FlagDraftPlayers, draftfixtures.TopCenter+","+draftfixtures.CenterWing)
	viper.Set(config.FlagDraftFormat, "CSV")
	viper.Set(config.FlagDraftScenario, "Conservative")
	viper.Set(config.FlagDraftLimit, 5)

	request, err := draftRankingsRequestFromFlags()
	require.NoError(t, err)
	assert.Equal(t, draftfixtures.LeagueKey, request.League.LeagueKey)
	assert.Equal(t, draftrank.FormatCSV, request.Format)
	assert.Equal(t, []string{"c", "LW"}, request.Query.Positions, "the shared view validates and normalizes positions")
	assert.Equal(t, []string{draftfixtures.TopCenter, draftfixtures.CenterWing}, request.Query.PlayerKeys)
	assert.Equal(t, draftrank.ScenarioConservative, request.Query.Scenario)
	assert.Equal(t, draftrank.SortOverallRank, request.Query.Sort)
	assert.Equal(t, 5, request.Query.Limit)
	assert.Equal(t, uuid.Nil, request.Snapshot)
}

func TestDraftRankingsRequestFromFlags_NumericLeagueUsesCurrentSeason(t *testing.T) {
	setDraftRankingDefaults()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagDraftLeague, "1001")
	request, err := draftRankingsRequestFromFlags()
	require.NoError(t, err)
	assert.Equal(t, draftrank.LeagueRef{Season: nhl.Current().StartYear(), LeagueID: draftfixtures.LeagueID}, request.League)
}

func TestDraftRankingsRequestFromFlags_Rejects(t *testing.T) {
	for name, set := range map[string]func(){
		"missing league": func() {},
		"bad format": func() {
			viper.Set(config.FlagDraftLeague, draftfixtures.LeagueKey)
			viper.Set(config.FlagDraftFormat, "xlsx")
		},
		"bad snapshot": func() {
			viper.Set(config.FlagDraftLeague, draftfixtures.LeagueKey)
			viper.Set(config.FlagDraftSnapshot, "latest")
		},
	} {
		t.Run(name, func(t *testing.T) {
			setDraftRankingDefaults()
			t.Cleanup(viper.Reset)
			set()
			_, err := draftRankingsRequestFromFlags()
			assert.Error(t, err)
		})
	}
}

// TestExportDraftRankings_MatchesTheServicePage checks CLI parity: the
// command writes exactly the page the shared service returns, which is also
// what the GraphQL resolvers convert.
func TestExportDraftRankings_MatchesTheServicePage(t *testing.T) {
	store := draftfixtures.NewStore()
	snapshot, err := draftfixtures.Snapshot(uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli")), draftfixtures.AsOf)
	require.NoError(t, err)
	store.Add(snapshot)
	now := func() time.Time { return draftfixtures.AsOf }
	service := draftrank.NewService(store, nil, draftrank.ServiceOptions{Now: now})
	request := draftRankingsRequest{
		League: draftrank.LeagueRef{LeagueKey: draftfixtures.LeagueKey},
		Query:  draftrank.Query{Positions: []string{"C", "LW"}},
	}
	page, err := draftrank.NewService(store, nil, draftrank.ServiceOptions{Now: now}).
		Rankings(context.Background(), request.League, uuid.Nil, request.Query)
	require.NoError(t, err)

	for _, format := range draftrank.Formats {
		request.Format = format
		var got, want, stderr bytes.Buffer
		require.NoError(t, exportDraftRankings(context.Background(), &got, &stderr, service, request))
		require.NoError(t, draftrank.Export(&want, page, format))
		assert.Equal(t, want.String(), got.String(), "format %s", format)
	}
	request.Format = draftrank.FormatJSON
	var out bytes.Buffer
	require.NoError(t, exportDraftRankings(context.Background(), &out, &bytes.Buffer{}, service, request))
	var decoded draftrank.Page
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	assert.Equal(t, 5, decoded.Total, "C/LW players are listed once each")
}

func TestExportDraftRankings_CSVReportsStateAndFailsWithoutSnapshot(t *testing.T) {
	store := draftfixtures.NewStore()
	service := draftrank.NewService(store, nil, draftrank.ServiceOptions{})
	request := draftRankingsRequest{League: draftrank.LeagueRef{LeagueKey: draftfixtures.LeagueKey}, Format: draftrank.FormatCSV}

	var out, stderr bytes.Buffer
	err := exportDraftRankings(context.Background(), &out, &stderr, service, request)
	require.ErrorIs(t, err, errNoRanking)
	assert.Contains(t, err.Error(), string(draftrank.IssueNotComputed))
	assert.Contains(t, stderr.String(), "status NOT_COMPUTED")

	snapshot, err := draftfixtures.Snapshot(uuid.NewSHA1(uuid.NameSpaceOID, []byte("csv")), draftfixtures.AsOf)
	require.NoError(t, err)
	store.Add(snapshot)
	stderr.Reset()
	require.NoError(t, exportDraftRankings(context.Background(), &bytes.Buffer{}, &stderr, service, request))
	assert.Contains(t, stderr.String(), string(draftrank.IssueNewsSourceStale), "a served snapshot's issues reach the CSV user too")
}

// ────────────────────────────────────────────────────────────────────────────
// refresh-draft-rankings sync step
// ────────────────────────────────────────────────────────────────────────────

func TestBuildRefreshDraftRankingsInput_Default(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	input, err := buildRefreshDraftRankingsInput()
	require.NoError(t, err)
	assert.Equal(t, &model.RefreshDraftRankingsInput{}, input)
}

func TestBuildRefreshDraftRankingsInput_Flags(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagDraftLeagues, "1001, 1002")
	viper.Set(config.FlagDraftBenchPolicy, "excluded")
	viper.Set(config.FlagDraftWorkloadCaps, true)
	viper.Set(config.FlagDraftUncertaintyPenalty, "0.25")

	input, err := buildRefreshDraftRankingsInput()
	require.NoError(t, err)
	assert.Equal(t, []int{draftfixtures.LeagueID, draftfixtures.OtherLeague}, input.LeagueIds)
	assert.Equal(t, model.DraftBenchPolicyExcluded, *input.BenchPolicy)
	assert.Equal(t, model.DraftWorkloadCapPolicyPerPlayer, *input.WorkloadCapPolicy)
	assert.InDelta(t, draftTestPenalty, *input.UncertaintyPenalty, 0)
}

func TestBuildRefreshDraftRankingsInput_Rejects(t *testing.T) {
	for flag, value := range map[string]string{
		config.FlagDraftLeagues:            "1001,x",
		config.FlagDraftBenchPolicy:        "sometimes",
		config.FlagDraftUncertaintyPenalty: "-1",
	} {
		viper.Reset()
		viper.Set(flag, value)
		_, err := buildRefreshDraftRankingsInput()
		assert.Error(t, err, flag)
	}
	viper.Reset()
}

func TestRefreshDraftRankingsSyncStep(t *testing.T) {
	news := slices.Index(config.AllSyncSteps, config.StepRefreshNews)
	rankings := slices.Index(config.AllSyncSteps, config.StepRefreshDraftRankings)
	require.GreaterOrEqual(t, rankings, 0)
	assert.Equal(t, news+1, rankings, "rankings refresh right after the news they are adjusted by")
	assert.Equal(t, []string{config.StepRefreshNews, config.StepRefreshDraftRankings}, config.SyncStepGroups["draft"])
	assert.Equal(t, "refreshDraftRankings", workflowRefreshDraftRankings.String())
}
