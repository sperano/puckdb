package yahoo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportActivities holds dependencies for Yahoo data import activities.
type ImportActivities struct {
	Storage  store.Storage
	GobCache *cache.GobCache
	Queries  Queries
}

// ImportYahooLeague imports a Yahoo league from cached XML into the database.
func (a *ImportActivities) ImportYahooLeague(ctx context.Context, input ImportYahooLeagueInput) (ImportYahooLeagueResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooLeague")()

	logger := activity.GetLogger(ctx)
	result := ImportYahooLeagueResult{}

	// Read the league file
	leagueRes := resource.League{Season: input.Season, LeagueID: input.LeagueID}
	if !a.Storage.Exists(ctx, leagueRes.Path()) {
		logger.Warn("No league file found in cache", "season", input.Season, "leagueID", input.LeagueID)
		return result, nil
	}

	fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, leagueRes)
	if err != nil {
		return result, fmt.Errorf("read league file: %w", err)
	}

	league := fantasy.League

	// Parse dates
	startDate := shared.ParseDateToPgDate(league.StartDate)
	endDate := shared.ParseDateToPgDate(league.EndDate)
	tradeEndDate := shared.ParseDateToPgDate(league.Settings.TradeEndDate)

	// Parse draft time
	var draftTime pgtype.Timestamptz
	if league.Settings.DraftTime > 0 {
		draftTime = pgtype.Timestamptz{
			Time:  time.Unix(int64(league.Settings.DraftTime), 0),
			Valid: true,
		}
	}

	// Upsert the league
	leagueParams := sqlcdb.UpsertYahooLeagueParams{
		ID:                    int32(league.ID),
		LeagueKey:             league.Key,
		Name:                  league.Name,
		Url:                   league.URL,
		LogoUrl:               league.LogoURL,
		Season:                int32(league.Season),
		GameCode:              league.GameCode,
		NumTeams:              int32(league.NumTeams),
		ScoringType:           league.ScoringType,
		LeagueType:            league.LeagueType,
		DraftStatus:           league.DraftStatus,
		IsProLeague:           league.IsProLeague == 1,
		IsCashLeague:          league.IsCashLeague == 1,
		StartDate:             startDate,
		EndDate:               endDate,
		DraftType:             league.Settings.DraftType,
		IsAuctionDraft:        league.Settings.IsAuctionDraft == 1,
		DraftTime:             draftTime,
		DraftPickTime:         pgtype.Int4{Int32: int32(league.Settings.DraftPickTime), Valid: league.Settings.DraftPickTime > 0},
		WaiverType:            league.Settings.WaiverType,
		WaiverRule:            league.Settings.WaiverRule,
		WaiverTime:            pgtype.Int4{Int32: int32(league.Settings.WaiverTime), Valid: league.Settings.WaiverTime > 0},
		TradeEndDate:          tradeEndDate,
		TradeRatifyType:       league.Settings.TradeRatifyType,
		TradeRejectTime:       pgtype.Int4{Int32: int32(league.Settings.TradeRejectTime), Valid: league.Settings.TradeRejectTime > 0},
		MaxTeams:              pgtype.Int4{Int32: int32(league.Settings.MaxTeams), Valid: league.Settings.MaxTeams > 0},
		PlayerPool:            league.Settings.PlayerPool,
		PostDraftPlayers:      league.Settings.PostDraftPlayers,
		CantCutList:           league.Settings.CantCutList,
		UsesPlayoff:           league.Settings.UsesPlayoff == 1,
		PersistentUrl:         league.Settings.PersistentURL,
		LeagueUpdateTimestamp: pgtype.Int8{Int64: int64(league.LeagueUpdateTimestamp), Valid: league.LeagueUpdateTimestamp > 0},
	}

	if err := a.Queries.UpsertYahooLeague(ctx, leagueParams); err != nil {
		return result, fmt.Errorf("upsert league %d: %w", league.ID, err)
	}

	// Batch upsert roster positions
	if len(league.Settings.RosterPositions.Slice) > 0 {
		posParams := make([]sqlcdb.UpsertYahooLeagueRosterPositionBatchParams, len(league.Settings.RosterPositions.Slice))
		for i, pos := range league.Settings.RosterPositions.Slice {
			posParams[i] = sqlcdb.UpsertYahooLeagueRosterPositionBatchParams{
				LeagueID:           int32(league.ID),
				Position:           pos.Position,
				PositionType:       pos.PositionType,
				Count:              int32(pos.Count),
				IsStartingPosition: pos.IsStartingPosition == 1,
			}
		}

		if err := shared.ExecBatch(a.Queries.UpsertYahooLeagueRosterPositionBatch(ctx, posParams), func(i int) string {
			return fmt.Sprintf("roster position %s", posParams[i].Position)
		}); err != nil {
			return result, err
		}
		result.RosterPositions = len(posParams)
	}

	// Batch upsert stat categories
	if len(league.Settings.StatCategories.Stats.Slice) > 0 {
		statParams := make([]sqlcdb.UpsertYahooLeagueStatCategoryBatchParams, len(league.Settings.StatCategories.Stats.Slice))
		for i, stat := range league.Settings.StatCategories.Stats.Slice {
			statParams[i] = sqlcdb.UpsertYahooLeagueStatCategoryBatchParams{
				LeagueID:  int32(league.ID),
				StatID:    int32(stat.StatID),
				Name:      stat.Name,
				Abbr:      stat.Abbr,
				StatGroup: stat.Group,
				Enabled:   stat.Enabled == 1,
				Value:     pgtype.Float4{}, // Yahoo doesn't provide point values in settings response
			}
		}

		if err := shared.ExecBatch(a.Queries.UpsertYahooLeagueStatCategoryBatch(ctx, statParams), func(i int) string {
			return fmt.Sprintf("stat category %d", statParams[i].StatID)
		}); err != nil {
			return result, err
		}
		result.StatCategories = len(statParams)
	}

	logger.Info("Imported Yahoo league",
		"season", input.Season,
		"leagueID", input.LeagueID,
		"rosterPositions", result.RosterPositions,
		"statCategories", result.StatCategories)

	return result, nil
}

// ImportYahooTeams imports Yahoo teams from cached XML into the database.
func (a *ImportActivities) ImportYahooTeams(ctx context.Context, input ImportYahooTeamsInput) (ImportYahooTeamsResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooTeams")()

	logger := activity.GetLogger(ctx)
	result := ImportYahooTeamsResult{}

	if len(input.Teams) == 0 {
		logger.Warn("No teams to import", "season", input.Season)
		return result, nil
	}

	// Collect all team and manager params
	var teamParams []sqlcdb.UpsertYahooTeamBatchParams
	var managerParams []sqlcdb.UpsertYahooTeamManagerBatchParams

	for _, teamInfo := range input.Teams {
		// Read the team file
		teamRes := resource.Team{Season: input.Season, LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID}
		if !a.Storage.Exists(ctx, teamRes.Path()) {
			logger.Warn("No team file found in cache",
				"season", input.Season,
				"leagueID", teamInfo.LeagueID,
				"teamID", teamInfo.TeamID)
			continue
		}

		fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, teamRes)
		if err != nil {
			return result, fmt.Errorf("read team file %d: %w", teamInfo.TeamID, err)
		}

		team := fantasy.Team

		// Extract logo URL (prefer large size)
		var logoURL string
		for _, logo := range team.TeamLogos.Slice {
			if logo.Size == "large" || logoURL == "" {
				logoURL = logo.URL
			}
		}

		teamParams = append(teamParams, sqlcdb.UpsertYahooTeamBatchParams{
			LeagueID:              int32(teamInfo.LeagueID),
			ID:                    int32(team.ID),
			TeamKey:               team.Key,
			Name:                  team.Name,
			Url:                   team.URL,
			LogoUrl:               logoURL,
			DraftPosition:         pgtype.Int4{Int32: int32(team.DraftPosition), Valid: team.DraftPosition > 0},
			WaiverPriority:        pgtype.Int4{Int32: int32(team.WaiverPriority), Valid: team.WaiverPriority > 0},
			NumberOfMoves:         int32(team.NumberOfMoves),
			NumberOfTrades:        int32(team.NumberOfTrades),
			IsOwnedByCurrentLogin: team.IsOwnedByCurrentLogin,
		})

		// Collect managers for this team
		for _, manager := range team.Managers.Slice {
			managerParams = append(managerParams, sqlcdb.UpsertYahooTeamManagerBatchParams{
				LeagueID:       int32(teamInfo.LeagueID),
				TeamID:         int32(team.ID),
				ID:             int32(manager.ID),
				Nickname:       manager.Nickname,
				Guid:           manager.GUID,
				Email:          manager.EMail,
				ImageUrl:       manager.ImageURL,
				FeloScore:      int32(manager.FeloScore),
				FeloTier:       manager.FeloTier,
				IsCurrentLogin: manager.IsCurrentLogin,
				IsCommissioner: false, // Not in the XML response, would need separate lookup
			})
		}
	}

	// Batch upsert teams
	if len(teamParams) > 0 {
		if err := shared.ExecBatch(a.Queries.UpsertYahooTeamBatch(ctx, teamParams), func(i int) string {
			return fmt.Sprintf("team %d", teamParams[i].ID)
		}); err != nil {
			return result, err
		}
		result.TeamsImported = len(teamParams)
	}

	// Batch upsert managers
	if len(managerParams) > 0 {
		if err := shared.ExecBatch(a.Queries.UpsertYahooTeamManagerBatch(ctx, managerParams), func(i int) string {
			return fmt.Sprintf("manager %d (team %d)", managerParams[i].ID, managerParams[i].TeamID)
		}); err != nil {
			return result, err
		}
		result.ManagersImported = len(managerParams)
	}

	logger.Info("Imported Yahoo teams",
		"season", input.Season,
		"teams", result.TeamsImported,
		"managers", result.ManagersImported)

	return result, nil
}

// ImportYahooDataForDate imports Yahoo team summaries and rosters for a single date.
func (a *ImportActivities) ImportYahooDataForDate(ctx context.Context, input ImportYahooDataForDateInput) (ImportYahooDataForDateResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooDataForDate")()

	logger := activity.GetLogger(ctx)
	result := ImportYahooDataForDateResult{Origins: core.OriginCounts{}}

	if len(input.Teams) == 0 {
		return result, nil
	}

	// Collect params for this date across all teams
	summaryParams := a.collectSummaryParams(ctx, input.Teams, input.Date, result.Origins)
	rosterParams := a.collectRosterParams(ctx, input.Teams, input.Date, result.Origins)

	// Batch upsert summaries (includes stats as columns)
	if len(summaryParams) > 0 {
		if err := upsertSummaries(ctx, a.Queries, summaryParams); err != nil {
			return result, err
		}
		result.SummariesImported = len(summaryParams)
	}

	// Batch upsert rosters
	if len(rosterParams) > 0 {
		if err := upsertRosters(ctx, a.Queries, rosterParams); err != nil {
			return result, err
		}
		result.RostersImported = len(rosterParams)
	}

	if result.SummariesImported > 0 || result.RostersImported > 0 {
		logger.Info("Imported Yahoo data for date",
			"date", input.Date.Format(config.DateFormat),
			"summaries", result.SummariesImported,
			"rosters", result.RostersImported)
	}

	return result, nil
}

// collectSummaryParams reads team summary files and returns params for summaries with stats as columns.
func (a *ImportActivities) collectSummaryParams(ctx context.Context, teams []TeamInfo, date time.Time, origins core.OriginCounts) []sqlcdb.UpsertYahooTeamSummaryBatchParams {
	var summaryParams []sqlcdb.UpsertYahooTeamSummaryBatchParams
	pgDate := pgtype.Date{Time: date, Valid: true}

	for _, teamInfo := range teams {
		summaryRes := resource.TeamSummary{LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID, Date: date}
		if !a.Storage.Exists(ctx, summaryRes.Path()) {
			continue
		}

		fantasy, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, summaryRes)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format(config.DateFormat)).
				Msg("Failed to read summary file")
			continue
		}
		origins.Record(origin)

		team := fantasy.Team
		teamStats := team.TeamStats

		p := sqlcdb.UpsertYahooTeamSummaryBatchParams{
			LeagueID:     int32(teamInfo.LeagueID),
			TeamID:       int32(teamInfo.TeamID),
			Date:         pgDate,
			CoverageType: teamStats.CoverageType,
		}

		for _, stat := range teamStats.Stats.Slice {
			statID, err := strconv.Atoi(stat.StatID)
			if err != nil {
				continue
			}
			val := parseStatValue(stat.Value)
			assignStatField(&p, statID, val)
		}

		summaryParams = append(summaryParams, p)
	}

	return summaryParams
}

// parseStatValue converts a Yahoo stat value string to a nullable float.
// Yahoo uses "-" for stats with no data (e.g., goalie stats for a skater-only day).
func parseStatValue(s string) pgtype.Float4 {
	if s == "-" || s == "" {
		return pgtype.Float4{}
	}
	v, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return pgtype.Float4{}
	}
	return pgtype.Float4{Float32: float32(v), Valid: true}
}

// assignStatField maps a Yahoo stat ID to the corresponding struct field.
func assignStatField(p *sqlcdb.UpsertYahooTeamSummaryBatchParams, statID int, val pgtype.Float4) {
	switch statID {
	case 1:
		p.Goals = val
	case 2:
		p.Assists = val
	case 3:
		p.Points = val
	case 4:
		p.PlusMinus = val
	case 5:
		p.PIM = val
	case 8:
		p.PPP = val
	case 14:
		p.SOG = val
	case 16:
		p.FaceoffsWon = val
	case 17:
		p.FaceoffsLost = val
	case 19:
		p.Wins = val
	case 22:
		p.GoalsAgainst = val
	case 23:
		p.GAA = val
	case 24:
		p.ShotsAgainst = val
	case 25:
		p.Saves = val
	case 26:
		p.SavePct = val
	case 27:
		p.Shutouts = val
	case 29:
		p.SHP = val
	case 30:
		p.GWG = val
	case 31:
		p.Hits = val
	case 32:
		p.Blocks = val
	}
}

// collectRosterParams reads team roster files and returns params for rosters.
func (a *ImportActivities) collectRosterParams(ctx context.Context, teams []TeamInfo, date time.Time, origins core.OriginCounts) []sqlcdb.UpsertYahooTeamRosterBatchParams {
	var rosterParams []sqlcdb.UpsertYahooTeamRosterBatchParams
	pgDate := pgtype.Date{Time: date, Valid: true}

	for _, teamInfo := range teams {
		rosterRes := resource.Roster{LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID, Date: date}
		if !a.Storage.Exists(ctx, rosterRes.Path()) {
			continue
		}

		fantasy, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, rosterRes)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format(config.DateFormat)).
				Msg("Failed to read roster file")
			continue
		}
		origins.Record(origin)

		team := fantasy.Team
		roster := team.Roster

		for _, player := range roster.Players.Slice {
			rosterParams = append(rosterParams, sqlcdb.UpsertYahooTeamRosterBatchParams{
				LeagueID:         int32(teamInfo.LeagueID),
				TeamID:           int32(teamInfo.TeamID),
				Date:             pgDate,
				PlayerID:         int32(player.ID),
				CoverageType:     roster.CoverageType,
				IsEditable:       roster.IsEditable,
				PlayerKey:        player.Key,
				SelectedPosition: player.SelectedPosition.Position,
				IsFlex:           player.SelectedPosition.IsFlex != 0,

				PlayerStatus:      pgtype.Text{String: player.Status, Valid: player.Status != ""},
				PlayerStatusFull:  pgtype.Text{String: player.StatusFull, Valid: player.StatusFull != ""},
				InjuryNote:        pgtype.Text{String: player.InjuryNote, Valid: player.InjuryNote != ""},
				OnDisabledList:    pgtype.Bool{Bool: player.OnDisabledList != 0, Valid: true},
				PositionType:      pgtype.Text{String: player.PositionType, Valid: player.PositionType != ""},
				DisplayPosition:   pgtype.Text{String: player.DisplayPosition, Valid: player.DisplayPosition != ""},
				PrimaryPosition:   pgtype.Text{String: player.PrimaryPosition, Valid: player.PrimaryPosition != ""},
				EligiblePositions: player.EligiblePositions,
				UniformNumber:     pgtype.Int4{Int32: int32(player.UniformNumber), Valid: player.UniformNumber != 0},
				EditorialTeamAbbr: pgtype.Text{String: player.EditorialTeamAbbr, Valid: player.EditorialTeamAbbr != ""},
			})
		}
	}

	return rosterParams
}

// upsertSummaries batch upserts team summary records.
func upsertSummaries(ctx context.Context, queries YahooDataUpserter, params []sqlcdb.UpsertYahooTeamSummaryBatchParams) error {
	return shared.ExecBatch(queries.UpsertYahooTeamSummaryBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("summary league_id=%d team_id=%d date=%s",
			params[i].LeagueID, params[i].TeamID, params[i].Date.Time.Format(config.DateFormat))
	})
}

// upsertRosters batch upserts team roster records.
func upsertRosters(ctx context.Context, queries YahooDataUpserter, params []sqlcdb.UpsertYahooTeamRosterBatchParams) error {
	return shared.ExecBatch(queries.UpsertYahooTeamRosterBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("roster league_id=%d team_id=%d date=%s player_id=%d",
			params[i].LeagueID, params[i].TeamID, params[i].Date.Time.Format(config.DateFormat), params[i].PlayerID)
	})
}

// ImportYahooLeagueData imports cached transactions, draft results, and matchups for a league.
func (a *ImportActivities) ImportYahooLeagueData(ctx context.Context, input ImportYahooLeagueDataInput) (ImportYahooLeagueDataResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooLeagueData")()

	logger := activity.GetLogger(ctx)
	result := ImportYahooLeagueDataResult{}

	// Import transactions
	txCount, err := a.importYahooTransactions(ctx, input)
	if err != nil {
		return result, fmt.Errorf("import transactions: %w", err)
	}
	result.TransactionsImported = txCount

	// Import draft results
	draftCount, err := a.importYahooDraftResults(ctx, input)
	if err != nil {
		return result, fmt.Errorf("import draft results: %w", err)
	}
	result.DraftPicksImported = draftCount

	// Import matchups
	matchupCount, err := a.importYahooMatchups(ctx, input)
	if err != nil {
		return result, fmt.Errorf("import matchups: %w", err)
	}
	result.MatchupsImported = matchupCount

	logger.Info("Imported Yahoo league data",
		"season", input.Season,
		"leagueID", input.LeagueID,
		"transactions", result.TransactionsImported,
		"draftPicks", result.DraftPicksImported,
		"matchups", result.MatchupsImported)

	return result, nil
}

// importYahooTransactions reads cached transaction data and upserts to the database.
func (a *ImportActivities) importYahooTransactions(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	res := resource.Transactions{Season: input.Season, LeagueID: input.LeagueID}
	if !a.Storage.Exists(ctx, res.Path()) {
		return 0, nil
	}

	fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
	if err != nil {
		return 0, fmt.Errorf("read transactions cache: %w", err)
	}

	txns := fantasy.League.Transactions.Slice
	if len(txns) == 0 {
		return 0, nil
	}

	txParams := make([]sqlcdb.UpsertYahooTransactionBatchParams, len(txns))
	var playerParams []sqlcdb.UpsertYahooTransactionPlayerBatchParams

	for i, tx := range txns {
		leagueID := int32(input.LeagueID)

		txParams[i] = sqlcdb.UpsertYahooTransactionBatchParams{
			LeagueID:       leagueID,
			TransactionKey: tx.TransactionKey,
			Type:           tx.Type,
			Timestamp:      pgtype.Int8{Int64: int64(tx.Timestamp), Valid: int64(tx.Timestamp) != 0},
			Status:         pgtype.Text{String: tx.Status, Valid: tx.Status != ""},
		}

		for _, p := range tx.Players.Slice {
			playerParams = append(playerParams, sqlcdb.UpsertYahooTransactionPlayerBatchParams{
				LeagueID:           leagueID,
				TransactionKey:     tx.TransactionKey,
				PlayerID:           int32(p.ID),
				PlayerKey:          p.Key,
				Type:               p.TransactionData.Type,
				SourceType:         p.TransactionData.SourceType,
				SourceTeamKey:      p.TransactionData.SourceTeamKey,
				DestinationType:    p.TransactionData.DestinationType,
				DestinationTeamKey: p.TransactionData.DestinationTeamKey,
			})
		}
	}

	if err := shared.ExecBatch(a.Queries.UpsertYahooTransactionBatch(ctx, txParams), func(i int) string {
		return fmt.Sprintf("transaction %s", txParams[i].TransactionKey)
	}); err != nil {
		return 0, err
	}

	if len(playerParams) > 0 {
		if err := shared.ExecBatch(a.Queries.UpsertYahooTransactionPlayerBatch(ctx, playerParams), func(i int) string {
			return fmt.Sprintf("transaction player %s player_id=%d", playerParams[i].TransactionKey, playerParams[i].PlayerID)
		}); err != nil {
			return 0, err
		}
	}

	return len(txParams), nil
}

// importYahooDraftResults reads cached draft result data and upserts to the database.
func (a *ImportActivities) importYahooDraftResults(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	res := resource.DraftResults{Season: input.Season, LeagueID: input.LeagueID}
	if !a.Storage.Exists(ctx, res.Path()) {
		return 0, nil
	}

	fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
	if err != nil {
		return 0, fmt.Errorf("read draft results cache: %w", err)
	}

	picks := fantasy.League.DraftResults.Slice
	if len(picks) == 0 {
		return 0, nil
	}

	params := make([]sqlcdb.UpsertYahooDraftResultBatchParams, 0, len(picks))
	for _, pick := range picks {
		teamID, err := parseYahooTeamKey(pick.TeamKey)
		if err != nil {
			log.Warn().Err(err).Str("team_key", pick.TeamKey).Msg("skipping draft pick with unparseable team key")
			continue
		}
		playerID, err := parseYahooPlayerKey(pick.PlayerKey)
		if err != nil {
			log.Warn().Err(err).Str("player_key", pick.PlayerKey).Msg("skipping draft pick with unparseable player key")
			continue
		}

		var cost pgtype.Int4
		if pick.Cost > 0 {
			cost = pgtype.Int4{Int32: int32(pick.Cost), Valid: true}
		}

		params = append(params, sqlcdb.UpsertYahooDraftResultBatchParams{
			LeagueID: int32(input.LeagueID),
			Round:    int32(pick.Round),
			Pick:     int32(pick.Pick),
			TeamID:   int32(teamID),
			PlayerID: int32(playerID),
			Cost:     cost,
		})
	}

	if len(params) == 0 {
		return 0, nil
	}

	if err := shared.ExecBatch(a.Queries.UpsertYahooDraftResultBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("draft pick round %d pick %d", params[i].Round, params[i].Pick)
	}); err != nil {
		return 0, err
	}
	return len(params), nil
}

// parseYahooTeamKey extracts the numeric team ID from a Yahoo team key.
// Format: "gamekey.l.leagueid.t.teamid" (e.g., "423.l.12345.t.1" → 1)
func parseYahooTeamKey(key string) (int, error) {
	parts := strings.Split(key, ".t.")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid team key: %s", key)
	}
	return strconv.Atoi(parts[1])
}

// parseYahooPlayerKey extracts the numeric player ID from a Yahoo player key.
// Format: "gamekey.p.playerid" (e.g., "423.p.6616" → 6616)
func parseYahooPlayerKey(key string) (int, error) {
	parts := strings.Split(key, ".p.")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid player key: %s", key)
	}
	return strconv.Atoi(parts[1])
}

// importYahooMatchups reads cached matchup data for all weeks and upserts to the database.
func (a *ImportActivities) importYahooMatchups(ctx context.Context, input ImportYahooLeagueDataInput) (int, error) {
	var totalImported int

	// Iterate every possible week up to maxMatchupWeeks, mirroring the fetcher's
	// continue-on-skip semantics (fetch_activities.go). The cache may be
	// non-contiguous — an individual week can be absent (e.g. a download that
	// failed mid-sequence) while later weeks are present — so skip absent weeks
	// rather than stopping at the first gap, which would silently under-import.
	for week := 1; week <= maxMatchupWeeks; week++ {
		res := resource.Matchups{Season: input.Season, LeagueID: input.LeagueID, Week: week}
		if !a.Storage.Exists(ctx, res.Path()) {
			continue // Week not cached; later weeks may still be present
		}

		fantasy, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
		if err != nil {
			return totalImported, fmt.Errorf("read matchups week %d cache: %w", week, err)
		}

		matchups := fantasy.League.Scoreboard.Matchups.Slice
		if len(matchups) == 0 {
			continue
		}

		params := make([]sqlcdb.UpsertYahooMatchupBatchParams, 0, len(matchups))
		for _, m := range matchups {
			if len(m.Teams.Slice) != 2 {
				continue // Skip malformed matchups
			}

			team1 := m.Teams.Slice[0]
			team2 := m.Teams.Slice[1]

			params = append(params, sqlcdb.UpsertYahooMatchupBatchParams{
				LeagueID:      int32(input.LeagueID),
				Week:          int32(m.Week),
				Team1ID:       int32(team1.TeamID),
				Team2ID:       int32(team2.TeamID),
				Team1Points:   pgtype.Float4{Float32: float32(team1.TeamPoints.Total), Valid: true},
				Team2Points:   pgtype.Float4{Float32: float32(team2.TeamPoints.Total), Valid: true},
				Status:        pgtype.Text{String: m.Status, Valid: m.Status != ""},
				IsPlayoffs:    m.IsPlayoffs != 0,
				IsConsolation: m.IsConsolation != 0,
			})
		}

		if len(params) == 0 {
			continue
		}

		if err := shared.ExecBatch(a.Queries.UpsertYahooMatchupBatch(ctx, params), func(i int) string {
			return fmt.Sprintf("matchup week %d team1 %d vs team2 %d", params[i].Week, params[i].Team1ID, params[i].Team2ID)
		}); err != nil {
			return totalImported, err
		}
		totalImported += len(params)
	}

	return totalImported, nil
}

// Ensure store.DraftResult is used for XML parsing (compile-time documentation).
var _ = store.DraftResult{}
