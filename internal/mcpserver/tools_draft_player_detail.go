package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/newsadjust"
)

const (
	draftPlayerDetailToolName = "get_draft_player_detail"
	draftPlayerKeysArg        = "player_keys"
	draftSnapshotIDArg        = "snapshot_id"

	maxDraftPlayerDetailPlayers = 10
	draftDetailFloatFormat      = 'g'
	draftDetailFloatDigits      = -1
	draftDetailFloatBitSize     = 64
	maxDraftDetailQuoteRunes    = 240
	maxDraftDetailOutputBytes   = 256 * 1024
)

type draftPlayerDetailSource interface {
	leagueKeyResolver
	Rankings(context.Context, draftrank.LeagueRef, uuid.UUID, draftrank.Query) (draftrank.Page, error)
}

func draftPlayerDetailTool(source draftPlayerDetailSource, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(draftPlayerDetailToolName,
			mcp.WithDescription(fmt.Sprintf("Get the full stored draft-ranking detail for up to %d players: every scenario placement, category contribution, ranking explanation, and news adjustment. Output is one metadata header followed by bounded CSV sections; no stored JSON is returned.", maxDraftPlayerDetailPlayers)),
			mcp.WithString(leagueArg, mcp.Required(), mcp.Description("Yahoo league key (465.l.1001) or numeric ID (1001); an ID is resolved within season")),
			mcp.WithNumber(seasonYearParam.name, mcp.Required(), mcp.Description("Season as a start year (2025) or a season ID (20252026)")),
			mcp.WithString(draftSnapshotIDArg, mcp.Description("Optional immutable ranking snapshot UUID; omit for the latest snapshot")),
			mcp.WithArray(draftPlayerKeysArg, mcp.Required(),
				mcp.Description(fmt.Sprintf("Yahoo player keys to compare (1-%d, e.g. 465.p.1234)", maxDraftPlayerDetailPlayers)),
				mcp.MinItems(1), mcp.MaxItems(maxDraftPlayerDetailPlayers),
				mcp.WithStringItems()),
		),
		Handler: guard.scopedKey(source, func(ctx context.Context, req mcp.CallToolRequest, ref draftrank.LeagueRef) (*mcp.CallToolResult, error) {
			keys, errResult := requiredDraftPlayerKeys(req, ref.LeagueKey)
			if errResult != nil {
				return errResult, nil
			}
			snapshotID, errResult := optionalDraftSnapshotID(req)
			if errResult != nil {
				return errResult, nil
			}
			page, err := source.Rankings(ctx, ref, snapshotID, draftrank.Query{PlayerKeys: keys})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return draftPlayerDetailResult(page, keys)
		}),
	}
}

func draftPlayerDetailResult(page draftrank.Page, requested []string) (*mcp.CallToolResult, error) {
	text, err := draftPlayerDetailText(page, requested)
	if err != nil {
		return nil, err
	}
	if len(text) > maxDraftDetailOutputBytes {
		return mcp.NewToolResultError(fmt.Sprintf(
			"draft player detail result is %d bytes; maximum is %d: request fewer player_keys",
			len(text), maxDraftDetailOutputBytes)), nil
	}
	return mcp.NewToolResultText(text), nil
}

func optionalDraftSnapshotID(req mcp.CallToolRequest) (uuid.UUID, *mcp.CallToolResult) {
	raw, errResult := optionalString(req, draftSnapshotIDArg)
	if errResult != nil || strings.TrimSpace(raw) == "" {
		return uuid.Nil, errResult
	}
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: want a UUID", draftSnapshotIDArg, formatRaw(raw)))
	}
	return id, nil
}

type draftDetailPlayerRow struct {
	PlayerKey               string `json:"player_key"`
	YahooPlayerID           int    `json:"yahoo_player_id"`
	NHLPlayerID             string `json:"nhl_player_id"`
	Name                    string `json:"name"`
	Team                    string `json:"team"`
	EligiblePositions       string `json:"eligible_positions"`
	RosterEligiblePositions string `json:"roster_eligible_positions"`
	Status                  string `json:"status"`
	StatusFull              string `json:"status_full"`
	InjuryNote              string `json:"injury_note"`
	BaselineUncertainty     string `json:"baseline_uncertainty"`
	Uncertainty             string `json:"uncertainty"`
}

type draftDetailPlacementRow struct {
	PlayerKey        string `json:"player_key"`
	Scenario         string `json:"scenario"`
	OverallRank      int    `json:"overall_rank"`
	PositionRanks    string `json:"position_ranks"`
	Tier             int    `json:"tier"`
	OfficialScore    string `json:"official_score"`
	AdjustedScore    string `json:"adjusted_score"`
	ReplacementValue string `json:"replacement_value"`
	Value            string `json:"value"`
	AdjustedValue    string `json:"adjusted_value"`
	Uncertainty      string `json:"uncertainty"`
}

type draftDetailContributionRow struct {
	PlayerKey   string `json:"player_key"`
	Scenario    string `json:"scenario"`
	StatID      int    `json:"stat_id"`
	Abbr        string `json:"abbr"`
	Stat        string `json:"stat"`
	Projected   string `json:"projected"`
	Official    string `json:"official"`
	Adjusted    string `json:"adjusted"`
	Weight      string `json:"weight"`
	Direction   string `json:"direction"`
	Opportunity string `json:"opportunity"`
	Explanation string `json:"explanation"`
}

type draftDetailExplanationRow struct {
	PlayerKey   string `json:"player_key"`
	Scenario    string `json:"scenario"`
	Ordinal     int    `json:"ordinal"`
	Explanation string `json:"explanation"`
}

type draftDetailReasonRow struct {
	PlayerKey        string    `json:"player_key"`
	EventID          string    `json:"event_id"`
	Version          int       `json:"version"`
	IncidentID       string    `json:"incident_id"`
	Type             string    `json:"type"`
	Status           string    `json:"status"`
	DurationKind     string    `json:"duration_kind"`
	DurationGames    int       `json:"duration_games"`
	RoleTeamID       string    `json:"role_team_id"`
	RoleIceTime      string    `json:"role_ice_time"`
	RolePowerPlay    string    `json:"role_power_play"`
	RoleGoalieRole   string    `json:"role_goalie_role"`
	EffectiveFrom    time.Time `json:"effective_from"`
	EffectiveUntil   time.Time `json:"effective_until"`
	Outcome          string    `json:"outcome"`
	Detail           string    `json:"detail"`
	Scenarios        string    `json:"scenarios"`
	LatestEvidenceAt time.Time `json:"latest_evidence_at"`
	AgeHours         string    `json:"age_hours"`
}

type draftDetailEvidenceRow struct {
	PlayerKey   string    `json:"player_key"`
	EventID     string    `json:"event_id"`
	VersionID   int64     `json:"version_id"`
	Publisher   string    `json:"publisher"`
	Kind        string    `json:"kind"`
	URL         string    `json:"url"`
	ReportedAt  time.Time `json:"reported_at"`
	RetrievedAt time.Time `json:"retrieved_at"`
	Quote       string    `json:"quote"`
}

type draftDetailEffectRow struct {
	PlayerKey          string `json:"player_key"`
	Scenario           string `json:"scenario"`
	MissedGames        string `json:"missed_games"`
	Availability       string `json:"availability"`
	GamesFactor        string `json:"games_factor"`
	IceTimeFactor      string `json:"ice_time_factor"`
	PowerPlayFactor    string `json:"power_play_factor"`
	GoalieStartsFactor string `json:"goalie_starts_factor"`
	TeamID             string `json:"team_id"`
}

type draftDetailChangeRow struct {
	PlayerKey    string `json:"player_key"`
	Stat         string `json:"stat"`
	Scenario     string `json:"scenario"`
	BaselineMean string `json:"baseline_mean"`
	BaselineLow  string `json:"baseline_low"`
	BaselineHigh string `json:"baseline_high"`
	AdjustedMean string `json:"adjusted_mean"`
	AdjustedLow  string `json:"adjusted_low"`
	AdjustedHigh string `json:"adjusted_high"`
}

type draftDetailOverrideRow struct {
	PlayerKey string `json:"player_key"`
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	EventID   string `json:"event_id"`
	Input     string `json:"input"`
	Scenario  string `json:"scenario"`
	Original  string `json:"original"`
	Value     string `json:"value"`
	Reason    string `json:"reason"`
}

type draftDetailNoteRow struct {
	PlayerKey string `json:"player_key"`
	Kind      string `json:"kind"`
	Message   string `json:"message"`
}

type draftDetailIssueRow struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type draftDetailRows struct {
	players       []draftDetailPlayerRow
	placements    []draftDetailPlacementRow
	contributions []draftDetailContributionRow
	explanations  []draftDetailExplanationRow
	reasons       []draftDetailReasonRow
	evidence      []draftDetailEvidenceRow
	effects       []draftDetailEffectRow
	changes       []draftDetailChangeRow
	overrides     []draftDetailOverrideRow
	notes         []draftDetailNoteRow
}

func draftPlayerDetailText(page draftrank.Page, requested []string) (string, error) {
	rows := collectDraftDetailRows(page.Rows)
	sections := []struct {
		title string
		rows  any
	}{
		{"players", rows.players}, {"placements", rows.placements}, {"contributions", rows.contributions},
		{"explanations", rows.explanations}, {"adjustment_reasons", rows.reasons}, {"adjustment_evidence", rows.evidence},
		{"adjustment_effects", rows.effects}, {"adjustment_changes", rows.changes}, {"adjustment_overrides", rows.overrides},
		{"adjustment_notes", rows.notes}, {draftIssuesSection, draftDetailIssues(page.Issues)},
	}
	parts := []string{draftDetailHeader(page, requested) + "\n"}
	for _, section := range sections {
		text, err := csvSection(section.title, section.rows)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, ""), nil
}

func draftDetailHeader(page draftrank.Page, requested []string) string {
	var snapshotID, snapshotAsOf, scenarios string
	if page.Snapshot != nil {
		snapshotID = page.Snapshot.ID.String()
		snapshotAsOf = timeCell(page.Snapshot.AsOf)
		scenarios = joinCell(page.Snapshot.Scenarios)
	}
	var latest string
	if page.LatestSnapshotID != uuid.Nil {
		latest = page.LatestSnapshotID.String()
	}
	var h metadataHeader
	h.add("league_key", page.League.LeagueKey)
	h.add("league_id", page.League.LeagueID)
	h.add("season", page.League.Season)
	h.add("league", page.League.Name)
	h.add("status", page.Status)
	h.add("snapshot_id", snapshotID)
	h.add("snapshot_as_of", snapshotAsOf)
	h.add("latest_snapshot_id", latest)
	h.add("player_keys", strings.Join(requested, cellListSeparator))
	h.add("players", page.Total)
	h.add("scenarios", scenarios)
	return h.String()
}

func collectDraftDetailRows(players []draftrank.Row) draftDetailRows {
	var out draftDetailRows
	for _, player := range players {
		out.players = append(out.players, newDraftDetailPlayerRow(player))
		addDraftPlacementRows(&out, player)
		addDraftAdjustmentRows(&out, player)
	}
	return out
}

func newDraftDetailPlayerRow(row draftrank.Row) draftDetailPlayerRow {
	out := draftDetailPlayerRow{
		PlayerKey: row.PlayerKey, YahooPlayerID: row.YahooPlayerID, Name: row.Name, Team: row.Team,
		EligiblePositions: joinCell(row.EligiblePositions), RosterEligiblePositions: joinCell(row.RosterEligiblePositions),
		Status: row.Status, StatusFull: row.StatusFull, InjuryNote: row.InjuryNote,
	}
	if row.NHLPlayerID != 0 {
		out.NHLPlayerID = strconv.FormatInt(row.NHLPlayerID, 10)
	}
	if row.Adjustment != nil {
		out.BaselineUncertainty = draftDetailFloat(row.Adjustment.BaselineUncertainty)
		out.Uncertainty = draftDetailFloat(row.Adjustment.Uncertainty)
	}
	return out
}

func addDraftPlacementRows(out *draftDetailRows, row draftrank.Row) {
	for _, scenario := range draftrank.Scenarios {
		placement, ok := row.Placements[scenario]
		if !ok {
			continue
		}
		out.placements = append(out.placements, newDraftDetailPlacementRow(row, placement))
		for _, contribution := range placement.Contributions {
			out.contributions = append(out.contributions, newDraftDetailContributionRow(row.PlayerKey, scenario, contribution))
		}
		for i, explanation := range placement.Explanations {
			out.explanations = append(out.explanations, draftDetailExplanationRow{
				PlayerKey: row.PlayerKey, Scenario: string(scenario), Ordinal: i + 1, Explanation: explanation,
			})
		}
	}
}

func newDraftDetailPlacementRow(row draftrank.Row, p draftrank.Placement) draftDetailPlacementRow {
	return draftDetailPlacementRow{
		PlayerKey: row.PlayerKey, Scenario: string(p.Scenario), OverallRank: p.OverallRank,
		PositionRanks: draftPositionRanks(row.EligiblePositions, p.PositionRanks), Tier: p.Tier,
		OfficialScore: draftDetailFloat(p.OfficialScore), AdjustedScore: draftDetailFloat(p.AdjustedScore),
		ReplacementValue: draftDetailFloat(p.ReplacementValue), Value: draftDetailFloat(p.Value),
		AdjustedValue: draftDetailFloat(p.AdjustedValue), Uncertainty: draftDetailFloat(p.Uncertainty),
	}
}

func newDraftDetailContributionRow(playerKey string, scenario draftrank.Scenario, c draftrank.Contribution) draftDetailContributionRow {
	return draftDetailContributionRow{
		PlayerKey: playerKey, Scenario: string(scenario), StatID: c.StatID, Abbr: c.Abbr, Stat: c.Stat,
		Projected: draftDetailFloat(c.Projected), Official: draftDetailFloat(c.Official), Adjusted: draftDetailFloat(c.Adjusted),
		Weight: draftDetailFloat(c.Weight), Direction: c.Direction, Opportunity: draftDetailFloat(c.Opportunity), Explanation: c.Explanation,
	}
}

func addDraftAdjustmentRows(out *draftDetailRows, row draftrank.Row) {
	a := row.Adjustment
	if a == nil {
		return
	}
	for _, reason := range a.Reasons {
		out.reasons = append(out.reasons, newDraftDetailReasonRow(row.PlayerKey, reason))
		for _, evidence := range reason.Evidence {
			out.evidence = append(out.evidence, newDraftDetailEvidenceRow(row.PlayerKey, reason.EventID, evidence))
		}
	}
	addDraftEffectRows(out, row.PlayerKey, a)
	addDraftChangeRows(out, row.PlayerKey, a)
	for _, override := range a.Overrides {
		out.overrides = append(out.overrides, newDraftDetailOverrideRow(row.PlayerKey, override))
	}
	for _, message := range a.Assumptions {
		out.notes = append(out.notes, draftDetailNoteRow{PlayerKey: row.PlayerKey, Kind: "assumption", Message: message})
	}
	for _, message := range a.Alerts {
		out.notes = append(out.notes, draftDetailNoteRow{PlayerKey: row.PlayerKey, Kind: "alert", Message: message})
	}
}

func newDraftDetailReasonRow(playerKey string, r newsadjust.Reason) draftDetailReasonRow {
	row := draftDetailReasonRow{
		PlayerKey: playerKey, EventID: r.EventID, Version: r.Version, Type: string(r.Type), Status: string(r.Status),
		DurationKind: string(r.Duration.Kind), DurationGames: r.Duration.Games, EffectiveFrom: r.EffectiveFrom.UTC(),
		EffectiveUntil: r.EffectiveUntil.UTC(), Outcome: string(r.Outcome), Detail: r.Detail,
		Scenarios: joinCell(r.Scenarios), LatestEvidenceAt: r.LatestEvidenceAt.UTC(), AgeHours: draftDetailFloat(r.AgeHours),
	}
	if r.IncidentID != 0 {
		row.IncidentID = strconv.FormatInt(r.IncidentID, 10)
	}
	if r.Role != nil {
		if r.Role.TeamID != nil {
			row.RoleTeamID = strconv.FormatInt(*r.Role.TeamID, 10)
		}
		row.RoleIceTime = string(r.Role.IceTime)
		row.RolePowerPlay = string(r.Role.PowerPlay)
		row.RoleGoalieRole = string(r.Role.GoalieRole)
	}
	return row
}

func newDraftDetailEvidenceRow(playerKey, eventID string, e newsadjust.EvidenceRef) draftDetailEvidenceRow {
	return draftDetailEvidenceRow{
		PlayerKey: playerKey, EventID: eventID, VersionID: e.VersionID, Publisher: e.Publisher, Kind: string(e.Kind), URL: e.URL,
		ReportedAt: e.ReportedAt.UTC(), RetrievedAt: e.RetrievedAt.UTC(), Quote: boundedText(e.Quote, maxDraftDetailQuoteRunes),
	}
}

func addDraftEffectRows(out *draftDetailRows, playerKey string, a *newsadjust.PlayerAdjustment) {
	for _, scenario := range newsadjust.Scenarios {
		effect, ok := a.Effects[scenario]
		if !ok {
			continue
		}
		teamID := ""
		if effect.TeamID != nil {
			teamID = strconv.FormatInt(*effect.TeamID, 10)
		}
		out.effects = append(out.effects, draftDetailEffectRow{
			PlayerKey: playerKey, Scenario: string(scenario), MissedGames: draftDetailFloat(effect.MissedGames),
			Availability: draftDetailFloat(effect.Availability), GamesFactor: draftDetailFloat(effect.GamesFactor),
			IceTimeFactor: draftDetailFloat(effect.IceTime), PowerPlayFactor: draftDetailFloat(effect.PowerPlay),
			GoalieStartsFactor: draftDetailFloat(effect.GoalieStarts), TeamID: teamID,
		})
	}
}

func addDraftChangeRows(out *draftDetailRows, playerKey string, a *newsadjust.PlayerAdjustment) {
	for _, change := range a.Changes {
		for _, scenario := range newsadjust.Scenarios {
			estimate, ok := change.Adjusted[scenario]
			if !ok {
				continue
			}
			out.changes = append(out.changes, draftDetailChangeRow{
				PlayerKey: playerKey, Stat: string(change.Stat), Scenario: string(scenario),
				BaselineMean: draftDetailFloat(change.Baseline.Mean), BaselineLow: draftDetailFloat(change.Baseline.Low),
				BaselineHigh: draftDetailFloat(change.Baseline.High), AdjustedMean: draftDetailFloat(estimate.Mean),
				AdjustedLow: draftDetailFloat(estimate.Low), AdjustedHigh: draftDetailFloat(estimate.High),
			})
		}
	}
}

func newDraftDetailOverrideRow(playerKey string, a newsadjust.AppliedOverride) draftDetailOverrideRow {
	return draftDetailOverrideRow{
		PlayerKey: playerKey, ID: a.Override.ID, Kind: string(a.Override.Kind), EventID: a.Override.EventID,
		Input: string(a.Override.Input), Scenario: string(a.Scenario), Original: draftDetailFloat(a.Original),
		Value: draftDetailFloat(a.Value), Reason: boundedText(a.Override.Reason, maxDraftDetailQuoteRunes),
	}
}

func draftPositionRanks(eligible []string, ranks map[string]int) string {
	values := make([]string, 0, len(ranks))
	for _, position := range eligible {
		if rank, ok := ranks[position]; ok {
			values = append(values, position+":"+strconv.Itoa(rank))
		}
	}
	return strings.Join(values, cellListSeparator)
}

func draftDetailFloat(value float64) string {
	return strconv.FormatFloat(value, draftDetailFloatFormat, draftDetailFloatDigits, draftDetailFloatBitSize)
}

func timeCell(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func draftDetailIssues(issues []draftrank.Issue) []draftDetailIssueRow {
	rows := make([]draftDetailIssueRow, 0, len(issues))
	for _, issue := range issues {
		rows = append(rows, draftDetailIssueRow{Code: string(issue.Code), Message: issue.Message})
	}
	return rows
}
