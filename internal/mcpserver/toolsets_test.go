package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseToolsets(t *testing.T) {
	tests := []struct {
		list string
		want []Toolset
	}{
		{"nhl", []Toolset{ToolsetNHL}},
		{"yahoo", []Toolset{ToolsetYahoo}},
		{"nhl,yahoo", []Toolset{ToolsetNHL, ToolsetYahoo}},
		{" Yahoo , NHL ", []Toolset{ToolsetNHL, ToolsetYahoo}},
		{"yahoo,yahoo,", []Toolset{ToolsetYahoo}},
	}
	for _, tt := range tests {
		t.Run(tt.list, func(t *testing.T) {
			got, err := ParseToolsets(tt.list)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseToolsetsRejects(t *testing.T) {
	tests := map[string]string{
		"":           "no toolset selected",
		" , ":        "no toolset selected",
		"nhl,espn":   `unknown toolset "espn" (valid: nhl, yahoo)`,
		"nhl;yahoo":  `unknown toolset "nhl;yahoo"`,
		"fantasy":    `unknown toolset "fantasy"`,
		"nhl, ,mlb ": `unknown toolset "mlb"`,
	}
	for list, want := range tests {
		t.Run(list, func(t *testing.T) {
			_, err := ParseToolsets(list)
			require.ErrorContains(t, err, want)
		})
	}
}

func TestServerName(t *testing.T) {
	assert.Equal(t, "puckdb-nhl", ServerName([]Toolset{ToolsetNHL}))
	assert.Equal(t, "puckdb-yahoo", ServerName([]Toolset{ToolsetYahoo}))
	assert.Equal(t, "puckdb-nhl-yahoo", ServerName([]Toolset{ToolsetNHL, ToolsetYahoo}))
}

func TestOptionsHasToolset(t *testing.T) {
	opts := Options{Toolsets: []Toolset{ToolsetYahoo}}
	assert.True(t, opts.HasToolset(ToolsetYahoo))
	assert.False(t, opts.HasToolset(ToolsetNHL))
}
