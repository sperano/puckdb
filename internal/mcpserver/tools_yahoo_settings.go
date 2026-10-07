package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// noRulesSnapshot is the rules_snapshot header value of a league whose
	// rules were never recorded in yahoo_league_rule_snapshots.
	noRulesSnapshot = "none"
	// Section titles of get_yahoo_league_settings.
	categoriesSection  = "scoring_categories"
	rosterSlotsSection = "roster_slots"
)

// errLeagueNotImported means yahoo_leagues has no row for the league ID.
var errLeagueNotImported = errors.New("league not imported")

// leagueSettings is what get_yahoo_league_settings reports for one league.
type leagueSettings struct {
	league     sqlcdb.YahooLeague
	snapshot   *draft.Snapshot // nil when no rules snapshot was recorded
	categories []sqlcdb.YahooLeagueStatCategory
	slots      []sqlcdb.YahooLeagueRosterPosition
}

// settingsCategoryRow is one scoring category without the league ID, which
// the header carries once.
type settingsCategoryRow struct {
	StatID       int32           `json:"stat_id"`
	Abbr         string          `json:"abbr"`
	Name         string          `json:"name"`
	Group        string          `json:"group"`
	PositionType string          `json:"position_type"`
	Direction    draft.Direction `json:"direction"`
	PointsWeight pgtype.Float4   `json:"points_weight"`
	Enabled      bool            `json:"enabled"`
	DisplayOnly  bool            `json:"display_only"`
}

// settingsSlotRow is one roster slot without the league ID.
type settingsSlotRow struct {
	Position     string `json:"position"`
	PositionType string `json:"position_type"`
	Count        int32  `json:"count"`
	Starting     bool   `json:"starting"`
}

func yahooLeagueSettingsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_league_settings",
			mcp.WithDescription("Get what a Yahoo fantasy league scores: a header summarizing its latest imported rules "+
				"(format, rules source, whether they are a temporary stand-in copied from an earlier season), then its scoring "+
				"categories (direction higher/lower is better, points weight, display-only) and roster slots as CSV."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
		),
		Handler: guard.scoped(func(ctx context.Context, _ mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			settings, err := loadLeagueSettings(ctx, q, leagueID)
			if errors.Is(err, errLeagueNotImported) {
				return unknownLeagueResult(leagueID), nil
			}
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := settings.text()
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		}),
	}
}

// loadLeagueSettings reads the league row, its latest rules snapshot for the
// league's own season, its stat categories and its roster positions.
func loadLeagueSettings(ctx context.Context, q yahooQueries, leagueID int32) (leagueSettings, error) {
	league, err := q.GetYahooLeague(ctx, leagueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return leagueSettings{}, errLeagueNotImported
	}
	if err != nil {
		return leagueSettings{}, fmt.Errorf("load league %d: %w", leagueID, err)
	}
	settings := leagueSettings{league: league}
	if settings.snapshot, err = latestRulesSnapshot(ctx, q, league); err != nil {
		return leagueSettings{}, err
	}
	if settings.categories, err = q.GetYahooLeagueStatCategories(ctx, leagueID); err != nil {
		return leagueSettings{}, fmt.Errorf("load stat categories of league %d: %w", leagueID, err)
	}
	if settings.slots, err = q.GetYahooLeagueRosterPositions(ctx, leagueID); err != nil {
		return leagueSettings{}, fmt.Errorf("load roster positions of league %d: %w", leagueID, err)
	}
	return settings, nil
}

// latestRulesSnapshot decodes the league's latest rules version the way the
// draft helper does (draft.SnapshotFromRow), or returns nil when none was
// recorded.
func latestRulesSnapshot(ctx context.Context, q yahooQueries, league sqlcdb.YahooLeague) (*draft.Snapshot, error) {
	row, err := q.GetLatestYahooLeagueRuleSnapshot(ctx, sqlcdb.GetLatestYahooLeagueRuleSnapshotParams{
		Season: league.Season, LeagueID: league.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load rules snapshot of league %d: %w", league.ID, err)
	}
	snapshot, err := draft.SnapshotFromRow(row)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// text renders the header line and the two CSV sections, each section
// ending in a newline, with no blank lines between them.
func (s leagueSettings) text() (string, error) {
	categories, err := csvSection(categoriesSection, s.categoryRows())
	if err != nil {
		return "", err
	}
	slots, err := csvSection(rosterSlotsSection, s.slotRows())
	if err != nil {
		return "", err
	}
	return s.header().String() + "\n" + categories + slots, nil
}

// header identifies the league and summarizes its rules snapshot. Without
// a snapshot, format falls back to the league row's scoring type.
func (s leagueSettings) header() metadataHeader {
	var h metadataHeader
	h.add("league_id", s.league.ID)
	h.add("league_key", s.league.LeagueKey)
	h.add("season", s.league.Season)
	h.add("name", s.league.Name)
	if s.snapshot == nil {
		h.add("format", s.league.ScoringType)
		h.add("num_teams", s.league.NumTeams)
		h.add("rules_snapshot", noRulesSnapshot)
		return h
	}
	snap := s.snapshot
	standIn := snap.Source == draft.SourceTemporaryStandIn
	h.add("format", snap.Rules.ScoringType)
	h.add("num_teams", snap.Rules.NumTeams)
	h.add("rules_source", snap.Source)
	h.add("stand_in", standIn)
	if standIn {
		h.add("source_season", snap.SourceSeason)
		h.add("source_league_key", snap.SourceLeagueKey)
	}
	h.add("rules_hash", snap.Hash)
	h.add("fetched_at", snap.FetchedAt.UTC().Format(time.RFC3339))
	h.add("last_seen_at", snap.LastSeenAt.UTC().Format(time.RFC3339))
	h.add("rules_issues", len(snap.Rules.Issues))
	return h
}

func (s leagueSettings) categoryRows() []settingsCategoryRow {
	rows := make([]settingsCategoryRow, 0, len(s.categories))
	for _, c := range s.categories {
		rows = append(rows, settingsCategoryRow{
			StatID: c.StatID, Abbr: c.Abbr, Name: c.Name, Group: c.StatGroup,
			PositionType: c.PositionType, Direction: draft.DirectionFromStoredSortOrder(c.SortOrder.Int16, c.SortOrder.Valid),
			PointsWeight: c.Value, Enabled: c.Enabled, DisplayOnly: c.IsOnlyDisplayStat,
		})
	}
	return rows
}

func (s leagueSettings) slotRows() []settingsSlotRow {
	rows := make([]settingsSlotRow, 0, len(s.slots))
	for _, p := range s.slots {
		rows = append(rows, settingsSlotRow{
			Position: p.Position, PositionType: p.PositionType, Count: p.Count, Starting: p.IsStartingPosition,
		})
	}
	return rows
}
