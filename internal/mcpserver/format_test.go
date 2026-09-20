package mcpserver

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resultText extracts the text content from a CallToolResult.
func resultText(t *testing.T, result *mcpgo.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content)
	tc, ok := mcpgo.AsTextContent(result.Content[0])
	require.True(t, ok, "expected TextContent")
	return tc.Text
}

// parseCSV parses all rows (including header) from a CSV string.
func parseCSV(t *testing.T, text string) [][]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(text))
	rows, err := r.ReadAll()
	require.NoError(t, err)
	return rows
}

// --- ResultJSON ---

func TestResultJSON_SimpleMap(t *testing.T) {
	t.Parallel()

	result, err := ResultJSON(map[string]any{"team_id": 8, "abbrev": "MTL"})
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, `"team_id"`)
	assert.Contains(t, text, `"MTL"`)
}

func TestResultJSON_Struct(t *testing.T) {
	t.Parallel()

	type payload struct {
		PlayerID int    `json:"player_id"`
		Name     string `json:"name"`
	}
	result, err := ResultJSON(payload{PlayerID: 8480321, Name: "Carey Price"})
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, `"player_id":8480321`)
	assert.Contains(t, text, `"Carey Price"`)
}

// --- ResultCSV: error cases ---

func TestResultCSV_NonSlice(t *testing.T) {
	t.Parallel()

	_, err := ResultCSV("not a slice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects a slice")
}

func TestResultCSV_EmptySlice(t *testing.T) {
	t.Parallel()

	type row struct {
		ID int `json:"id"`
	}
	result, err := ResultCSV([]row{})
	require.NoError(t, err)
	assert.Equal(t, "no results", resultText(t, result))
}

// --- ResultCSV: headers ---

func TestResultCSV_Headers(t *testing.T) {
	t.Parallel()

	type row struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		//lint:ignore U1000 deliberately never set — pins that ResultCSV skips unexported fields
		unexported string
		Skip       string `json:"-"`
		NoTag      string
	}
	rows := []row{{ID: 1, FullName: "Carey Price"}}
	result, err := ResultCSV(rows)
	require.NoError(t, err)
	records := parseCSV(t, resultText(t, result))
	// Only exported, tagged, non-skipped fields
	assert.Equal(t, []string{"id", "full_name"}, records[0])
}

// --- ResultCSV: pgtype nullable types ---

func TestResultCSV_PgtypeText(t *testing.T) {
	t.Parallel()

	type row struct {
		Name pgtype.Text `json:"name"`
		Null pgtype.Text `json:"null_val"`
	}
	rows := []row{{
		Name: pgtype.Text{String: "Carey Price", Valid: true},
		Null: pgtype.Text{Valid: false},
	}}
	records := parseCSV(t, resultText(t, mustCSV(t, rows)))
	fields := records[1]
	assert.Equal(t, "Carey Price", fields[0])
	assert.Equal(t, "", fields[1])
}

func TestResultCSV_PgtypeInt(t *testing.T) {
	t.Parallel()

	type row struct {
		I2   pgtype.Int2 `json:"i2"`
		I4   pgtype.Int4 `json:"i4"`
		I8   pgtype.Int8 `json:"i8"`
		Null pgtype.Int4 `json:"null_val"`
	}
	rows := []row{{
		I2:   pgtype.Int2{Int16: 16, Valid: true},
		I4:   pgtype.Int4{Int32: 99, Valid: true},
		I8:   pgtype.Int8{Int64: 8480321, Valid: true},
		Null: pgtype.Int4{Valid: false},
	}}
	records := parseCSV(t, resultText(t, mustCSV(t, rows)))
	fields := records[1]
	assert.Equal(t, "16", fields[0])
	assert.Equal(t, "99", fields[1])
	assert.Equal(t, "8480321", fields[2])
	assert.Equal(t, "", fields[3])
}

func TestResultCSV_PgtypeFloat(t *testing.T) {
	t.Parallel()

	type row struct {
		F4   pgtype.Float4 `json:"f4"`
		F8   pgtype.Float8 `json:"f8"`
		Null pgtype.Float8 `json:"null_val"`
	}
	rows := []row{{
		F4:   pgtype.Float4{Float32: 2.5, Valid: true},
		F8:   pgtype.Float8{Float64: 3.14, Valid: true},
		Null: pgtype.Float8{Valid: false},
	}}
	fields := parseCSV(t, resultText(t, mustCSV(t, rows)))[1]
	assert.Equal(t, "2.5", fields[0])
	assert.Equal(t, "3.14", fields[1])
	assert.Equal(t, "", fields[2])
}

func TestResultCSV_PgtypeBool(t *testing.T) {
	t.Parallel()

	type row struct {
		Active   pgtype.Bool `json:"active"`
		Inactive pgtype.Bool `json:"inactive"`
		Null     pgtype.Bool `json:"null_val"`
	}
	rows := []row{{
		Active:   pgtype.Bool{Bool: true, Valid: true},
		Inactive: pgtype.Bool{Bool: false, Valid: true},
		Null:     pgtype.Bool{Valid: false},
	}}
	fields := parseCSV(t, resultText(t, mustCSV(t, rows)))[1]
	assert.Equal(t, "true", fields[0])
	assert.Equal(t, "false", fields[1])
	assert.Equal(t, "", fields[2])
}

func TestResultCSV_PgtypeDate(t *testing.T) {
	t.Parallel()

	type row struct {
		Birth pgtype.Date `json:"birth"`
		Null  pgtype.Date `json:"null_val"`
	}
	d := pgtype.Date{}
	require.NoError(t, d.Scan("1987-08-16"))
	rows := []row{{Birth: d, Null: pgtype.Date{Valid: false}}}
	fields := parseCSV(t, resultText(t, mustCSV(t, rows)))[1]
	assert.Equal(t, "1987-08-16", fields[0])
	assert.Equal(t, "", fields[1])
}

func TestResultCSV_PgtypeTimestamptz(t *testing.T) {
	t.Parallel()

	type row struct {
		At   pgtype.Timestamptz `json:"at"`
		Null pgtype.Timestamptz `json:"null_val"`
	}
	rows := []row{{
		At:   pgtype.Timestamptz{Time: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC), Valid: true},
		Null: pgtype.Timestamptz{Valid: false},
	}}
	fields := parseCSV(t, resultText(t, mustCSV(t, rows)))[1]
	assert.Equal(t, "2024-01-15T10:30:00Z", fields[0])
	assert.Equal(t, "", fields[1])
}

// --- ResultCSV: nullable enum (Null* structs) ---

func TestResultCSV_NullPlayerPosition(t *testing.T) {
	t.Parallel()

	type row struct {
		Position sqlcdb.NullPlayerPosition `json:"position"`
		Null     sqlcdb.NullPlayerPosition `json:"null_pos"`
	}
	rows := []row{{
		Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true},
		Null:     sqlcdb.NullPlayerPosition{Valid: false},
	}}
	fields := parseCSV(t, resultText(t, mustCSV(t, rows)))[1]
	assert.Equal(t, "G", fields[0])
	assert.Equal(t, "", fields[1])
}

func TestResultCSV_AllPositionValues(t *testing.T) {
	t.Parallel()

	type row struct {
		Pos sqlcdb.NullPlayerPosition `json:"pos"`
	}
	positions := []sqlcdb.PlayerPosition{
		sqlcdb.PlayerPositionC,
		sqlcdb.PlayerPositionLW,
		sqlcdb.PlayerPositionRW,
		sqlcdb.PlayerPositionF,
		sqlcdb.PlayerPositionD,
		sqlcdb.PlayerPositionG,
	}
	var rows []row
	for _, p := range positions {
		rows = append(rows, row{Pos: sqlcdb.NullPlayerPosition{PlayerPosition: p, Valid: true}})
	}
	records := parseCSV(t, resultText(t, mustCSV(t, rows)))
	for i, p := range positions {
		assert.Equal(t, string(p), records[i+1][0])
	}
}

// --- ResultCSV: standard pointer types ---

func TestResultCSV_NilPointers(t *testing.T) {
	t.Parallel()

	s := "hello"
	i32 := int32(7)
	i64 := int64(99)
	type row struct {
		Str  *string `json:"str"`
		I32  *int32  `json:"i32"`
		I64  *int64  `json:"i64"`
		NilS *string `json:"nil_str"`
	}
	rows := []row{{Str: &s, I32: &i32, I64: &i64, NilS: nil}}
	fields := parseCSV(t, resultText(t, mustCSV(t, rows)))[1]
	assert.Equal(t, "hello", fields[0])
	assert.Equal(t, "7", fields[1])
	assert.Equal(t, "99", fields[2])
	assert.Equal(t, "", fields[3])
}

// --- ResultCSV: multi-row, CSV quoting ---

func TestResultCSV_MultipleRows(t *testing.T) {
	t.Parallel()

	type row struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	rows := []row{
		{ID: 1, Name: "Montreal Canadiens"},
		{ID: 2, Name: "Toronto Maple Leafs"},
		{ID: 3, Name: "Boston Bruins"},
	}
	records := parseCSV(t, resultText(t, mustCSV(t, rows)))
	require.Len(t, records, 4) // header + 3 rows
	assert.Equal(t, []string{"id", "name"}, records[0])
	assert.Equal(t, []string{"1", "Montreal Canadiens"}, records[1])
	assert.Equal(t, []string{"3", "Boston Bruins"}, records[3])
}

func TestResultCSV_CommaInValue(t *testing.T) {
	t.Parallel()

	type row struct {
		Name string `json:"name"`
	}
	rows := []row{{Name: "Price, Carey"}}
	text := resultText(t, mustCSV(t, rows))
	// CSV library should quote the value containing a comma
	records := parseCSV(t, text)
	assert.Equal(t, "Price, Carey", records[1][0])
}

// --- helper ---

func mustCSV(t *testing.T, items any) *mcpgo.CallToolResult {
	t.Helper()
	result, err := ResultCSV(items)
	require.NoError(t, err)
	return result
}
