package worker

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportYahooTransactions_NoFile(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	a := &SeasonsActivities{Storage: mem}

	count, err := a.importYahooTransactions(context.Background(), ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportYahooTransactions_EmptyTransactions(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	a := &SeasonsActivities{Storage: mem}

	// Write valid XML with empty transactions list
	xml := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><league><transactions count="0"></transactions></league></fantasy_content>`)
	res := resource.Transactions{Season: 2023, LeagueID: 12345}
	require.NoError(t, mem.Write(res.Path(), xml))

	count, err := a.importYahooTransactions(context.Background(), ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportYahooDraftResults_NoFile(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	a := &SeasonsActivities{Storage: mem}

	count, err := a.importYahooDraftResults(context.Background(), ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportYahooDraftResults_EmptyResults(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	a := &SeasonsActivities{Storage: mem}

	xml := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><league><draft_results count="0"></draft_results></league></fantasy_content>`)
	res := resource.DraftResults{Season: 2023, LeagueID: 12345}
	require.NoError(t, mem.Write(res.Path(), xml))

	count, err := a.importYahooDraftResults(context.Background(), ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportYahooDraftResults_SkipsInvalidKeys(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	a := &SeasonsActivities{Storage: mem}

	// Draft results with invalid team/player keys — should all be skipped
	xml := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><league><draft_results count="2">
  <draft_result><round>1</round><pick>1</pick><team_key>invalid</team_key><player_key>423.p.6616</player_key></draft_result>
  <draft_result><round>1</round><pick>2</pick><team_key>423.l.12345.t.1</team_key><player_key>invalid</player_key></draft_result>
</draft_results></league></fantasy_content>`)
	res := resource.DraftResults{Season: 2023, LeagueID: 12345}
	require.NoError(t, mem.Write(res.Path(), xml))

	count, err := a.importYahooDraftResults(context.Background(), ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportYahooMatchups_NoFile(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	a := &SeasonsActivities{Storage: mem}

	count, err := a.importYahooMatchups(context.Background(), ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportYahooMatchups_EmptyMatchups(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	a := &SeasonsActivities{Storage: mem}

	// Write week 1 with empty matchups, no week 2 → stops after week 1
	xml := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><league><scoreboard><week>1</week><matchups count="0"></matchups></scoreboard></league></fantasy_content>`)
	res := resource.Matchups{Season: 2023, LeagueID: 12345, Week: 1}
	require.NoError(t, mem.Write(res.Path(), xml))

	count, err := a.importYahooMatchups(context.Background(), ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportYahooMatchups_SkipsMalformedMatchups(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	a := &SeasonsActivities{Storage: mem}

	// Matchup with only 1 team (malformed) — should be skipped
	xml := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><league><scoreboard><week>1</week><matchups count="1">
  <matchup><week>1</week><teams><team><team_id>1</team_id><team_points><total>50.5</total></team_points></team></teams></matchup>
</matchups></scoreboard></league></fantasy_content>`)
	res := resource.Matchups{Season: 2023, LeagueID: 12345, Week: 1}
	require.NoError(t, mem.Write(res.Path(), xml))

	count, err := a.importYahooMatchups(context.Background(), ImportYahooLeagueDataInput{Season: 2023, LeagueID: 12345})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
