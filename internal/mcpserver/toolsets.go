package mcpserver

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sperano/puckdb/internal/draftrank"
)

// Toolset names a group of tools one server instance exposes, so public NHL
// data and private Yahoo league data can be served to different clients.
type Toolset string

const (
	// ToolsetNHL holds the public NHL data tools.
	ToolsetNHL Toolset = "nhl"
	// ToolsetYahoo holds the Yahoo fantasy league tools: league names,
	// managers, rosters, matchups, draft results and draft ranking state.
	ToolsetYahoo Toolset = "yahoo"
)

// allToolsets lists every toolset in canonical order; registration and the
// server name follow it whatever order the flag gave.
var allToolsets = []Toolset{ToolsetNHL, ToolsetYahoo}

const (
	// toolsetListSeparator separates toolsets in --mcp-toolsets.
	toolsetListSeparator = ","
	// serverNamePrefix starts every server name; the toolsets follow it.
	serverNamePrefix = "puckdb"
	// serverNameSeparator joins the prefix and toolset names.
	serverNameSeparator = "-"
)

// Options selects what one MCP server instance serves.
type Options struct {
	// Toolsets lists the tool groups to register, in canonical order.
	Toolsets []Toolset
	// YahooLeagues lists the Yahoo league keys the yahoo toolset serves;
	// empty serves every league.
	YahooLeagues []string
	// Draft is the draft ranking read path the yahoo toolset's draft tools
	// share with GraphQL and the CLI; required with the yahoo toolset.
	Draft *draftrank.Service
}

// HasToolset reports whether opts registers ts.
func (o Options) HasToolset(ts Toolset) bool {
	return slices.Contains(o.Toolsets, ts)
}

// validate rejects options NewServer cannot serve.
func (o Options) validate() error {
	if len(o.Toolsets) == 0 {
		return errors.New("no toolset selected")
	}
	for _, ts := range o.Toolsets {
		if !slices.Contains(allToolsets, ts) {
			return fmt.Errorf("unknown toolset %q (valid: %s)", ts, validToolsetNames())
		}
	}
	if o.HasToolset(ToolsetYahoo) && o.Draft == nil {
		return fmt.Errorf("the %s toolset needs a draft ranking service", ToolsetYahoo)
	}
	return nil
}

// ParseToolsets reads a comma-separated toolset list such as "nhl,yahoo".
// Names are case-insensitive and may repeat; the result is deduplicated and
// in canonical order. An empty list is an error: a server without tools is
// a misconfiguration.
func ParseToolsets(list string) ([]Toolset, error) {
	selected := make(map[Toolset]bool)
	for _, field := range strings.Split(list, toolsetListSeparator) {
		name := Toolset(strings.ToLower(strings.TrimSpace(field)))
		if name == "" {
			continue
		}
		if !slices.Contains(allToolsets, name) {
			return nil, fmt.Errorf("unknown toolset %q (valid: %s)", strings.TrimSpace(field), validToolsetNames())
		}
		selected[name] = true
	}
	var toolsets []Toolset
	for _, ts := range allToolsets {
		if selected[ts] {
			toolsets = append(toolsets, ts)
		}
	}
	if len(toolsets) == 0 {
		return nil, fmt.Errorf("no toolset selected (valid: %s)", validToolsetNames())
	}
	return toolsets, nil
}

// ServerName is the MCP server name for toolsets, e.g. "puckdb-nhl",
// "puckdb-yahoo" or "puckdb-nhl-yahoo", so clients list instances apart.
func ServerName(toolsets []Toolset) string {
	parts := []string{serverNamePrefix}
	for _, ts := range toolsets {
		parts = append(parts, string(ts))
	}
	return strings.Join(parts, serverNameSeparator)
}

func validToolsetNames() string {
	names := make([]string, len(allToolsets))
	for i, ts := range allToolsets {
		names[i] = string(ts)
	}
	return strings.Join(names, ", ")
}
