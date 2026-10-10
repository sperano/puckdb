package mcpserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/draftrank"
)

// draftLeagueSource is the draft ranking read path get_draft_leagues uses;
// *draftrank.Service satisfies it.
type draftLeagueSource interface {
	Leagues(ctx context.Context, season int, configured []int) ([]draftrank.LeagueSummary, error)
	League(ctx context.Context, ref draftrank.LeagueRef) (draftrank.LeagueSummary, error)
}

const (
	draftLeaguesToolName = "get_draft_leagues"
	// draftLeaguesSection and draftIssuesSection title the two CSV
	// sections of get_draft_leagues.
	draftLeaguesSection = "leagues"
	draftIssuesSection  = "issues"
	// cellListSeparator joins several values (issue codes, scenarios) in
	// one CSV cell.
	cellListSeparator = ";"
	// seasonIDYearFactor splits a season ID (20252026) into its start
	// year (2025) and end year (2026).
	seasonIDYearFactor = 10000
	// firstNHLStartYear is the start year of firstNHLSeasonID.
	firstNHLStartYear = firstNHLSeasonID / seasonIDYearFactor
	// lastStartYear is the largest season argument read as a start year;
	// larger values are read as season IDs.
	lastStartYear = seasonIDYearFactor - 1
)

// draftSeasonParam is a draft season: a start year or a season ID, both
// mapped to the start year the draft tables are keyed by.
var draftSeasonParam = intArg{name: "season", min: firstNHLStartYear, max: lastSeasonID}

func draftLeaguesTool(source draftLeagueSource, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(draftLeaguesToolName,
			mcp.WithDescription("List the season's Yahoo leagues that have imported rules, with their draft ranking state. "+
				"Start here before reading a league's draft rankings. "+
				"Prints a '# season=... leagues=...' header, then a '# leagues' CSV section: one row per league with its rules "+
				"(format, provisional, rules source), status (READY = a ranking snapshot is served, NOT_COMPUTED, REFRESHING, FAILED), "+
				"the latest snapshot (id, as-of, pool size, scenarios), the latest refresh attempt and its issue codes. "+
				"A '# issues' CSV section then gives every issue's code and message per league (stale snapshot or pool, "+
				"provisional rules, failing news sources, failed refresh, ...). "+
				"A league without imported rules is not listed: run a Yahoo sync for it."),
			mcp.WithNumber(draftSeasonParam.name, mcp.Description(
				"Season as a start year (2025) or a season ID (20252026); default: the current season")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := draftSeason(req)
			if errResult != nil {
				return errResult, nil
			}
			summaries, err := servedDraftLeagues(ctx, source, guard, season)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := draftLeaguesText(season, summaries)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		},
	}
}

// draftSeason reads the optional season argument as a start year. The draft
// tables are keyed by start year while every other tool takes a season ID,
// so both forms are accepted; anything else is refused rather than
// returning an empty list.
func draftSeason(req mcp.CallToolRequest) (int, *mcp.CallToolResult) {
	value, given, errResult := optionalInt[int64](req, draftSeasonParam)
	if errResult != nil {
		return 0, errResult
	}
	if !given {
		return nhl.Current().StartYear(), nil
	}
	if value <= lastStartYear {
		return int(value), nil
	}
	season, err := nhl.SeasonFromInt64(value)
	if err != nil || value < firstNHLSeasonID {
		example := nhl.Current()
		return 0, mcp.NewToolResultError(fmt.Sprintf("invalid %s %d: want a start year (%d) or a season ID (%d)",
			draftSeasonParam.name, value, example.StartYear(), example.ID()))
	}
	return season.StartYear(), nil
}

// servedDraftLeagues summarizes the season's leagues the guard serves. An
// unrestricted guard lists every league with imported rules. A restricted
// one reads only its allowlisted keys, so a league outside the list is
// never read; a listed key without imported rules is skipped like an
// unlisted league.
func servedDraftLeagues(ctx context.Context, source draftLeagueSource, guard leagueGuard, season int) ([]draftrank.LeagueSummary, error) {
	if !guard.restricted() {
		return source.Leagues(ctx, season, nil)
	}
	var summaries []draftrank.LeagueSummary
	for _, key := range guard.leagueKeys() {
		summary, err := source.League(ctx, draftrank.LeagueRef{LeagueKey: key})
		if errors.Is(err, draftrank.ErrUnknownLeague) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if summary.League.Season == season {
			summaries = append(summaries, summary)
		}
	}
	slices.SortFunc(summaries, func(a, b draftrank.LeagueSummary) int { return cmp.Compare(a.League.LeagueID, b.League.LeagueID) })
	return summaries, nil
}

// draftLeagueRow is one league's ranking state.
type draftLeagueRow struct {
	LeagueID          int       `json:"league_id"`
	LeagueKey         string    `json:"league_key"`
	Name              string    `json:"name"`
	NumTeams          int       `json:"num_teams"`
	ScoringType       string    `json:"scoring_type"`
	Format            string    `json:"format"`
	Provisional       bool      `json:"provisional"`
	RulesSource       string    `json:"rules_source"`
	Status            string    `json:"status"`
	SnapshotID        string    `json:"snapshot_id"`
	SnapshotAsOf      time.Time `json:"snapshot_as_of"`
	PoolSize          *int64    `json:"pool_size"`
	Scenarios         string    `json:"scenarios"`
	RefreshState      string    `json:"refresh_state"`
	RefreshCode       string    `json:"refresh_code"`
	RefreshStartedAt  time.Time `json:"refresh_started_at"`
	RefreshFinishedAt time.Time `json:"refresh_finished_at"`
	Issues            string    `json:"issues"`
}

// draftIssueRow is one issue of one league.
type draftIssueRow struct {
	LeagueKey string `json:"league_key"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

func newDraftLeagueRow(s draftrank.LeagueSummary) draftLeagueRow {
	row := draftLeagueRow{
		LeagueID: s.League.LeagueID, LeagueKey: s.League.LeagueKey, Name: s.League.Name, NumTeams: s.League.NumTeams,
		ScoringType: s.League.ScoringType, Format: s.League.Format, Provisional: s.League.Provisional,
		RulesSource: s.League.RulesSource, Status: string(s.Status),
	}
	if s.Snapshot != nil {
		poolSize := int64(s.Snapshot.PoolSize)
		row.SnapshotID, row.SnapshotAsOf, row.PoolSize = s.Snapshot.ID.String(), s.Snapshot.AsOf, &poolSize
		row.Scenarios = joinCell(s.Snapshot.Scenarios)
	}
	if s.Refresh != nil {
		row.RefreshState, row.RefreshCode = string(s.Refresh.State), string(s.Refresh.Code)
		row.RefreshStartedAt, row.RefreshFinishedAt = s.Refresh.StartedAt, s.Refresh.FinishedAt
	}
	codes := make([]draftrank.IssueCode, len(s.Issues))
	for i, issue := range s.Issues {
		codes[i] = issue.Code
	}
	row.Issues = joinCell(codes)
	return row
}

// joinCell joins values into one CSV cell.
func joinCell[T ~string](values []T) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return strings.Join(parts, cellListSeparator)
}

// draftLeaguesText renders the header and the leagues and issues sections.
func draftLeaguesText(season int, summaries []draftrank.LeagueSummary) (string, error) {
	leagues := make([]draftLeagueRow, 0, len(summaries))
	var issues []draftIssueRow
	for _, s := range summaries {
		leagues = append(leagues, newDraftLeagueRow(s))
		for _, issue := range s.Issues {
			issues = append(issues, draftIssueRow{LeagueKey: s.League.LeagueKey, Code: string(issue.Code), Message: issue.Message})
		}
	}
	var h metadataHeader
	h.add("season", season)
	h.add("leagues", len(leagues))
	leagueSection, err := csvSection(draftLeaguesSection, leagues)
	if err != nil {
		return "", err
	}
	issueSection, err := csvSection(draftIssuesSection, issues)
	if err != nil {
		return "", err
	}
	return h.String() + "\n" + leagueSection + issueSection, nil
}
