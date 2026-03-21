package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// PlayByPlayUpserter is the interface for database operations needed by play-by-play import.
type PlayByPlayUpserter interface {
	UpsertPlayEventBatch(ctx context.Context, arg []sqlcdb.UpsertPlayEventBatchParams) *sqlcdb.UpsertPlayEventBatchBatchResults
}

// ImportPlayByPlayForDateInput contains the parameters for importing play-by-play data for a date.
type ImportPlayByPlayForDateInput struct {
	DateSeasonInput
}

// ImportPlayByPlayForDateResult contains the results of importing play-by-play data.
type ImportPlayByPlayForDateResult struct {
	GamesProcessed int `json:"gamesProcessed"`
	EventsImported int `json:"eventsImported"`
}

// ImportPlayByPlayForDate imports play-by-play data for all games on a given date.
func (a *SeasonsActivities) ImportPlayByPlayForDate(ctx context.Context, input ImportPlayByPlayForDateInput) (ImportPlayByPlayForDateResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportPlayByPlayForDate", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)

	result := ImportPlayByPlayForDateResult{}

	scheduleRes := resource.DailySchedule{Date: input.Date}
	if !a.Storage.Exists(scheduleRes.Path()) {
		return result, nil
	}

	schedule, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, scheduleRes)
	if err != nil {
		return result, fmt.Errorf("read daily schedule: %w", err)
	}

	for _, game := range schedule.Games {
		if shouldSkipGame(game) {
			continue
		}

		pbpRes := resource.PlayByPlay{Date: input.Date, GameID: game.ID}
		if !a.Storage.Exists(pbpRes.Path()) {
			return result, fmt.Errorf("play-by-play file missing for game %s", game.ID.String())
		}

		pbp, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, pbpRes)
		if err != nil {
			return result, fmt.Errorf("read play-by-play for game %s: %w", game.ID.String(), err)
		}

		if len(pbp.Plays) == 0 {
			continue
		}

		params := make([]sqlcdb.UpsertPlayEventBatchParams, len(pbp.Plays))
		for i, play := range pbp.Plays {
			params[i] = playEventToParams(game.ID, play)
		}

		var firstErr error
		var errIdx int
		results := a.ImportQueries.UpsertPlayEventBatch(ctx, params)
		results.Exec(func(i int, err error) {
			if err != nil && firstErr == nil {
				firstErr = err
				errIdx = i
			}
		})

		if firstErr != nil {
			return result, fmt.Errorf("upsert play event %d for game %s: %w",
				pbp.Plays[errIdx].EventID, game.ID.String(), firstErr)
		}

		result.GamesProcessed++
		result.EventsImported += len(pbp.Plays)
	}

	logger.Debug("Imported play-by-play for date",
		"date", input.Date.Format("2006-01-02"),
		"games", result.GamesProcessed,
		"events", result.EventsImported)

	return result, nil
}

func playEventToParams(gameID nhl.GameID, play nhl.PlayEvent) sqlcdb.UpsertPlayEventBatchParams {
	p := sqlcdb.UpsertPlayEventBatchParams{
		GameID:        int64(gameID),
		EventID:       play.EventID,
		Period:        int32(play.PeriodDescriptor.Number),
		PeriodType:    string(play.PeriodDescriptor.PeriodType),
		TimeInPeriod:  play.TimeInPeriod,
		TimeRemaining: play.TimeRemaining,
		TypeCode:      int32(play.TypeCode),
		TypeDescKey:   string(play.TypeDescKey),
		SortOrder:     int32(play.SortOrder),
	}

	if play.SituationCode != "" {
		p.SituationCode = pgtype.Text{String: play.SituationCode, Valid: true}
	}
	if play.HomeTeamDefendingSide != "" {
		p.HomeTeamDefendingSide = pgtype.Text{String: string(play.HomeTeamDefendingSide), Valid: true}
	}

	if d := play.Details; d != nil {
		if d.XCoord != nil {
			p.XCoord = pgtype.Int4{Int32: int32(*d.XCoord), Valid: true}
		}
		if d.YCoord != nil {
			p.YCoord = pgtype.Int4{Int32: int32(*d.YCoord), Valid: true}
		}
		if d.ZoneCode != nil {
			p.ZoneCode = pgtype.Text{String: string(*d.ZoneCode), Valid: true}
		}
		if d.EventOwnerTeamID != nil {
			p.EventOwnerTeamID = pgtype.Int8{Int64: int64(*d.EventOwnerTeamID), Valid: true}
		}
		if d.ShotType != nil {
			p.ShotType = pgtype.Text{String: *d.ShotType, Valid: true}
		}
		if d.ShootingPlayerID != nil {
			p.ShootingPlayerID = pgtype.Int8{Int64: int64(*d.ShootingPlayerID), Valid: true}
		}
		if d.GoalieInNetID != nil {
			p.GoalieInNetID = pgtype.Int8{Int64: int64(*d.GoalieInNetID), Valid: true}
		}
		if d.BlockingPlayerID != nil {
			p.BlockingPlayerID = pgtype.Int8{Int64: int64(*d.BlockingPlayerID), Valid: true}
		}
		if d.ScoringPlayerID != nil {
			p.ScoringPlayerID = pgtype.Int8{Int64: int64(*d.ScoringPlayerID), Valid: true}
		}
		if d.ScoringPlayerTotal != nil {
			p.ScoringPlayerTotal = pgtype.Int4{Int32: int32(*d.ScoringPlayerTotal), Valid: true}
		}
		if d.Assist1PlayerID != nil {
			p.Assist1PlayerID = pgtype.Int8{Int64: int64(*d.Assist1PlayerID), Valid: true}
		}
		if d.Assist1PlayerTotal != nil {
			p.Assist1PlayerTotal = pgtype.Int4{Int32: int32(*d.Assist1PlayerTotal), Valid: true}
		}
		if d.Assist2PlayerID != nil {
			p.Assist2PlayerID = pgtype.Int8{Int64: int64(*d.Assist2PlayerID), Valid: true}
		}
		if d.Assist2PlayerTotal != nil {
			p.Assist2PlayerTotal = pgtype.Int4{Int32: int32(*d.Assist2PlayerTotal), Valid: true}
		}
		if d.AwayScore != nil {
			p.AwayScore = pgtype.Int4{Int32: int32(*d.AwayScore), Valid: true}
		}
		if d.HomeScore != nil {
			p.HomeScore = pgtype.Int4{Int32: int32(*d.HomeScore), Valid: true}
		}
		if d.HighlightClip != nil {
			p.HighlightClipID = pgtype.Int8{Int64: *d.HighlightClip, Valid: true}
		}
		if d.HighlightClipSharingURL != nil {
			p.HighlightClipUrl = pgtype.Text{String: *d.HighlightClipSharingURL, Valid: true}
		}
		if d.DiscreteClip != nil {
			p.DiscreteClipID = pgtype.Int8{Int64: *d.DiscreteClip, Valid: true}
		}
		if d.TypeCode != nil {
			p.PenaltyTypeCode = pgtype.Text{String: *d.TypeCode, Valid: true}
		}
		if d.DescKey != nil {
			p.PenaltyDescKey = pgtype.Text{String: *d.DescKey, Valid: true}
		}
		if d.Duration != nil {
			p.PenaltyDuration = pgtype.Int4{Int32: int32(*d.Duration), Valid: true}
		}
		if d.CommittedByPlayerID != nil {
			p.CommittedByPlayerID = pgtype.Int8{Int64: int64(*d.CommittedByPlayerID), Valid: true}
		}
		if d.DrawnByPlayerID != nil {
			p.DrawnByPlayerID = pgtype.Int8{Int64: int64(*d.DrawnByPlayerID), Valid: true}
		}
		if d.HittingPlayerID != nil {
			p.HittingPlayerID = pgtype.Int8{Int64: int64(*d.HittingPlayerID), Valid: true}
		}
		if d.HitteePlayerID != nil {
			p.HitteePlayerID = pgtype.Int8{Int64: int64(*d.HitteePlayerID), Valid: true}
		}
		if d.WinningPlayerID != nil {
			p.WinningPlayerID = pgtype.Int8{Int64: int64(*d.WinningPlayerID), Valid: true}
		}
		if d.LosingPlayerID != nil {
			p.LosingPlayerID = pgtype.Int8{Int64: int64(*d.LosingPlayerID), Valid: true}
		}
		if d.PlayerID != nil {
			p.PlayerID = pgtype.Int8{Int64: int64(*d.PlayerID), Valid: true}
		}
		if d.Reason != nil {
			p.Reason = pgtype.Text{String: *d.Reason, Valid: true}
		}
		if d.AwaySOG != nil {
			p.AwaySog = pgtype.Int4{Int32: int32(*d.AwaySOG), Valid: true}
		}
		if d.HomeSOG != nil {
			p.HomeSog = pgtype.Int4{Int32: int32(*d.HomeSOG), Valid: true}
		}
	}

	return p
}
