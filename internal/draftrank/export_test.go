package draftrank_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixturePage(t *testing.T, q draftrank.Query) draftrank.Page {
	t.Helper()
	store := draftfixtures.NewStore()
	store.Add(fixtureSnapshot(t))
	page, err := newService(store, serviceNow).Rankings(context.Background(), fixtureRef(), uuid.Nil, q)
	require.NoError(t, err)
	return page
}

func csvRecords(t *testing.T, page draftrank.Page) []map[string]string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, draftrank.Export(&buf, page, draftrank.FormatCSV))
	lines, err := csv.NewReader(&buf).ReadAll()
	require.NoError(t, err)
	header := lines[0]
	records := make([]map[string]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		record := make(map[string]string, len(header))
		for i, column := range header {
			record[column] = line[i]
		}
		records = append(records, record)
	}
	return records
}

func parseFloat(t *testing.T, value string) float64 {
	t.Helper()
	parsed, err := strconv.ParseFloat(value, 64)
	require.NoError(t, err)
	return parsed
}

func TestExport_CSVKeepsExactValuesAndNamesTheSnapshot(t *testing.T) {
	page := fixturePage(t, draftrank.Query{Positions: []string{"C", "LW"}})
	records := csvRecords(t, page)
	require.Len(t, records, len(page.Rows))
	for i, row := range page.Rows {
		record := records[i]
		assert.Equal(t, page.Snapshot.ID.String(), record["snapshot_id"])
		assert.Equal(t, page.Snapshot.Identity, record["snapshot_identity"])
		assert.Equal(t, "base", record["scenario"])
		assert.Equal(t, row.PlayerKey, record["player_key"])
		assert.Equal(t, strconv.Itoa(row.Placement.OverallRank), record["overall_rank"])
		assert.Equal(t, strconv.Itoa(row.PositionRank), record["position_rank"])
		assert.Equal(t, row.Placement.Value, parseFloat(t, record["value"]))
		assert.Equal(t, row.Placement.AdjustedValue, parseFloat(t, record["adjusted_value"]))
		assert.Equal(t, row.Placement.OfficialScore, parseFloat(t, record["official_score"]))
		assert.Equal(t, strings.Join(row.EligiblePositions, "|"), record["positions"])
	}
	dual := records[indexOfKey(page.Rows, draftfixtures.CenterWing)]
	assert.Regexp(t, `^C:\d+\|LW:\d+$`, dual["position_ranks"])
	assert.Contains(t, dual, "g_projected")
	assert.Contains(t, dual, "w_value")
	suspended := records[indexOfKey(page.Rows, draftfixtures.SuspendedKey)]
	assert.Equal(t, "1", suspended["news_reasons"])
}

func indexOfKey(rows []draftrank.Row, key string) int {
	for i, row := range rows {
		if row.PlayerKey == key {
			return i
		}
	}
	return -1
}

func TestExport_JSONRoundTripsThePage(t *testing.T) {
	page := fixturePage(t, draftrank.Query{Sort: draftrank.SortValue, Limit: 4})
	var buf bytes.Buffer
	require.NoError(t, draftrank.Export(&buf, page, draftrank.FormatJSON))
	var decoded draftrank.Page
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Len(t, decoded.Rows, len(page.Rows))
	for i := range page.Rows {
		assert.Equal(t, page.Rows[i].PlayerKey, decoded.Rows[i].PlayerKey)
		assert.Equal(t, page.Rows[i].Placement, decoded.Rows[i].Placement)
		assert.Equal(t, page.Rows[i].Placements, decoded.Rows[i].Placements)
	}
	assert.Equal(t, page.Snapshot.ID, decoded.Snapshot.ID)
	assert.Equal(t, page.Snapshot.Versions, decoded.Snapshot.Versions)
	assert.Equal(t, page.Issues, decoded.Issues)
	assert.Equal(t, page.Total, decoded.Total)
}

func TestExport_TableShowsContextAndRows(t *testing.T) {
	page := fixturePage(t, draftrank.Query{Search: "stutzle"})
	var buf bytes.Buffer
	require.NoError(t, draftrank.Export(&buf, page, draftrank.FormatTable))
	out := buf.String()
	assert.Contains(t, out, draftfixtures.LeagueName)
	assert.Contains(t, out, page.Snapshot.ID.String())
	assert.Contains(t, out, string(draftrank.IssueNewsSourceStale))
	assert.Contains(t, out, draftfixtures.AccentName)
	assert.Contains(t, out, "Showing 1 of 1")

	var empty bytes.Buffer
	require.NoError(t, draftrank.Export(&empty, fixturePage(t, draftrank.Query{Search: "nobody"}), draftrank.FormatTable))
	assert.Contains(t, empty.String(), "No players match.")
}

func TestExport_RejectsUnknownFormat(t *testing.T) {
	err := draftrank.Export(&bytes.Buffer{}, draftrank.Page{}, "xlsx")
	assert.ErrorIs(t, err, draftrank.ErrInvalidQuery)
}
