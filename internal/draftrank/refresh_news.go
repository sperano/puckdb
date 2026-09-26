package draftrank

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/sperano/puckdb/internal/projection"
)

// nhlSeasonGames is the number of regular-season games each NHL team
// plays, which news adjustments use to place event dates on the schedule.
const nhlSeasonGames = 82

// errNoSeasonDates means the target season's dates are not imported.
var errNoSeasonDates = errors.New("season dates are not imported")

// adjustedNews is the news side of a refresh. result is nil when news
// adjustments were unavailable; unavailable then says why.
type adjustedNews struct {
	result      *newsadjust.Result
	runID       uuid.UUID
	coverage    []news.SourceCoverage
	warnings    []string
	unavailable []Issue
}

// adjust applies the validated news events and overrides known at asOf to
// the baseline and stores the run. It reads stored extractions only; it
// never calls an LLM. Missing season dates or an adjustment that cannot be
// applied leave the snapshot with its baseline ranking and an explicit
// NEWS_ADJUSTMENTS_UNAVAILABLE issue rather than failing the refresh.
func (r *Refresher) adjust(ctx context.Context, req RefreshRequest, leagueKey string, baseline projection.Snapshot, baselineID uuid.UUID, asOf time.Time) (adjustedNews, error) {
	var out adjustedNews
	states, err := news.LoadFetchStates(ctx, r.Queries)
	if err != nil {
		return out, err
	}
	out.coverage = news.EvaluateCoverage(req.Sources, states, req.Season, asOf)
	season, err := r.season(ctx, req.Season)
	if errors.Is(err, errNoSeasonDates) {
		out.unavailable = append(out.unavailable, Issue{Code: IssueNewsAdjustmentsDown, Message: err.Error()})
		return out, nil
	}
	if err != nil {
		return out, err
	}
	events, warnings, err := r.Adjustments.LoadEvents(ctx, baseline)
	if err != nil {
		return out, err
	}
	overrides, err := r.Adjustments.ListOverrides(ctx)
	if err != nil {
		return out, err
	}
	result, err := newsadjust.Apply(newsadjust.Request{
		Baseline: baseline, Events: events, Overrides: overrides, Policy: newsadjust.DefaultPolicy(),
		Season: season, AsOf: asOf, LeagueKey: leagueKey, CoverageWarnings: newsadjust.CoverageWarnings(out.coverage),
	})
	if err != nil {
		out.unavailable = append(out.unavailable, Issue{Code: IssueNewsAdjustmentsDown, Message: "news adjustment failed: " + err.Error()})
		return out, nil
	}
	out.runID, err = r.Adjustments.SaveRun(ctx, baselineID, result)
	if err != nil {
		return out, err
	}
	out.result, out.warnings = &result, warnings
	return out, nil
}

// season reads the target season's regular-season dates.
func (r *Refresher) season(ctx context.Context, startYear int) (newsadjust.Season, error) {
	row, err := r.Queries.GetSeason(ctx, int32(SeasonID(startYear)))
	if errors.Is(err, pgx.ErrNoRows) {
		return newsadjust.Season{}, fmt.Errorf("%w: season %d is not in the seasons table; fetch the NHL season first", errNoSeasonDates, SeasonID(startYear))
	}
	if err != nil {
		return newsadjust.Season{}, fmt.Errorf("load season %d: %w", SeasonID(startYear), err)
	}
	if !row.StandingsStart.Valid || !row.StandingsEnd.Valid || !row.StandingsEnd.Time.After(row.StandingsStart.Time) {
		return newsadjust.Season{}, fmt.Errorf("%w: season %d has no valid start and end dates", errNoSeasonDates, SeasonID(startYear))
	}
	return newsadjust.Season{
		Start: row.StandingsStart.Time.UTC(),
		End:   row.StandingsEnd.Time.UTC().Add(projectionAsOfPrecision),
		Games: nhlSeasonGames,
	}, nil
}
