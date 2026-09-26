package draftrank

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// RefreshTimeout bounds one league refresh. A running attempt older
	// than this is reported as interrupted.
	RefreshTimeout = 30 * time.Minute
	// projectionAsOfPrecision is the granularity of the baseline
	// projection's as-of time. Projections read whole game days, so every
	// refresh of a day shares one stored baseline snapshot.
	projectionAsOfPrecision = 24 * time.Hour
)

// RefreshRequest asks for one league's snapshot. RunID identifies the
// refresh run (the Temporal run ID); Keep is how many snapshots of the
// league to keep (all when not positive); Sources are the enabled news
// sources whose coverage the snapshot reports.
type RefreshRequest struct {
	RunID    string
	Season   int
	LeagueID int
	Options  Options
	Keep     int
	Sources  []news.Source
}

// Refresher computes and stores league snapshots. Heartbeat, when set, is
// called between steps so a long refresh stays observable and cancellable.
type Refresher struct {
	Queries     *sqlcdb.Queries
	Projections *projection.Repository
	Adjustments *newsadjust.Repository
	Store       *PGStore
	Now         func() time.Time
	Heartbeat   func(step string)
}

// NewRefresher returns a Refresher over the pool.
func NewRefresher(pool *pgxpool.Pool) *Refresher {
	return &Refresher{
		Queries: sqlcdb.New(pool), Projections: projection.NewRepository(pool),
		Adjustments: newsadjust.NewRepository(pool), Store: NewPGStore(pool), Now: time.Now,
	}
}

// Refresh records an attempt, computes and stores the league's snapshot,
// and records the outcome, failures included. A failed or canceled attempt
// stores nothing, so the league keeps serving its last good snapshot.
func (r *Refresher) Refresh(ctx context.Context, req RefreshRequest) (Refresh, error) {
	started := r.now()
	id, err := r.Store.StartRefresh(ctx, req.RunID, req.Season, req.LeagueID, started)
	if err != nil {
		return Refresh{}, err
	}
	snapshotID, leagueKey, refreshErr := r.refresh(ctx, req, started)
	outcome := Refresh{ID: id, RunID: req.RunID, State: RefreshSucceeded, SnapshotID: snapshotID, StartedAt: started, FinishedAt: r.now()}
	var coded *RefreshError
	switch {
	case refreshErr == nil:
	case ctx.Err() != nil:
		outcome.State, outcome.Code, outcome.Error = RefreshCanceled, IssueRefreshCanceled, ctx.Err().Error()
	case errors.As(refreshErr, &coded):
		outcome.State, outcome.Code, outcome.Error = RefreshFailed, coded.Code, coded.Err.Error()
	default:
		outcome.State, outcome.Code, outcome.Error = RefreshFailed, IssueInternalError, refreshErr.Error()
	}
	// The outcome is recorded even when the refresh was canceled.
	if err := r.Store.FinishRefresh(context.WithoutCancel(ctx), id, outcome, leagueKey); err != nil {
		return outcome, errors.Join(refreshErr, err)
	}
	return outcome, refreshErr
}

func (r *Refresher) refresh(ctx context.Context, req RefreshRequest, asOf time.Time) (uuid.UUID, string, error) {
	rules, err := draft.LoadSnapshot(ctx, r.Queries, req.Season, req.LeagueID)
	if errors.Is(err, draft.ErrNoRules) {
		return uuid.Nil, "", refreshError(IssueMissingRules, err)
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	leagueKey := rules.Rules.LeagueKey
	if _, err := draft.ScoringFor(rules); err != nil {
		return uuid.Nil, leagueKey, refreshError(IssueUnsupportedScoring, err)
	}
	pool, poolNotes, err := draft.LoadLeaguePool(ctx, r.Queries, rules)
	if err != nil {
		return uuid.Nil, leagueKey, err
	}
	if len(pool) == 0 {
		return uuid.Nil, leagueKey, refreshError(IssueMissingPool, missingPoolError(rules, leagueKey, poolNotes))
	}
	r.heartbeat("projections")
	baselineID, baseline, err := r.baseline(ctx, req.Season, rules, pool, asOf)
	if err != nil {
		return uuid.Nil, leagueKey, err
	}
	r.heartbeat("news")
	adjusted, err := r.adjust(ctx, req, leagueKey, baseline, baselineID, asOf)
	if err != nil {
		return uuid.Nil, leagueKey, err
	}
	r.heartbeat("rankings")
	snapshot, err := Build(BuildInput{
		Rules: rules, Pool: pool, Baseline: baseline, BaselineID: baselineID,
		Adjustment: adjusted.result, AdjustmentRunID: adjusted.runID, AdjustmentWarnings: adjusted.warnings,
		Options: req.Options, News: adjusted.coverage, Unavailable: adjusted.unavailable, AsOf: asOf,
		PoolNotes: poolNotes,
	})
	if err != nil {
		return uuid.Nil, leagueKey, err
	}
	r.heartbeat("store")
	id, err := r.Store.SaveSnapshot(ctx, snapshot, req.Keep)
	return id, leagueKey, err
}

// missingPoolError explains an empty pool. A TEMPORARY stand-in league
// (draft.SourceTemporaryStandIn) has no Yahoo pool to sync at all — its pool
// notes say what NHL rosters were tried and why nothing qualified — so it
// gets a distinct message from a real Yahoo league's.
func missingPoolError(rules draft.Snapshot, leagueKey string, poolNotes []string) error {
	if rules.Source != draft.SourceTemporaryStandIn {
		return fmt.Errorf("league %s has no draftable players; run a Yahoo sync", leagueKey)
	}
	msg := fmt.Sprintf("league %s has no draftable players: no NHL roster players qualified; "+
		"import NHL rosters for the season or the prior season", leagueKey)
	if len(poolNotes) > 0 {
		msg += ": " + strings.Join(poolNotes, "; ")
	}
	return errors.New(msg)
}

// baseline builds (or, for a day already built, reuses) the league-neutral
// projection snapshot of the pool.
func (r *Refresher) baseline(ctx context.Context, season int, rules draft.Snapshot, pool []draft.PoolPlayer, asOf time.Time) (uuid.UUID, projection.Snapshot, error) {
	request := projection.BuildRequest{PlayerPool: projectionPool(pool), Categories: projectionCategories(rules.Rules)}
	id, snapshot, err := r.Projections.BuildSnapshot(ctx, projection.DefaultConfig(), SeasonID(season), asOf.UTC().Truncate(projectionAsOfPrecision), request)
	var coverage *projection.CoverageError
	if errors.As(err, &coverage) {
		return uuid.Nil, projection.Snapshot{}, refreshError(IssueMissingProjections, err)
	}
	if err != nil {
		return uuid.Nil, projection.Snapshot{}, err
	}
	return id, snapshot, nil
}

// projectionPool maps the Yahoo pool to projection pool players: a matched
// player takes their NHL history, an unmatched one stays with missing stats.
func projectionPool(pool []draft.PoolPlayer) []projection.PoolPlayer {
	out := make([]projection.PoolPlayer, 0, len(pool))
	for _, p := range pool {
		player := projection.PoolPlayer{PlayerKey: p.PlayerKey, Kind: projection.PlayerKindSkater}
		for _, position := range ViewPositions {
			if player.Position == "" && slices.Contains(p.EligiblePositions, position) {
				player.Position = position
			}
		}
		if slices.Contains(p.EligiblePositions, draft.PositionGoalie) {
			player.Kind, player.Position = projection.PlayerKindGoalie, draft.PositionGoalie
		}
		if p.Matched() {
			nhlID := p.NHLPlayerID
			player.PlayerID = &nhlID
		}
		out = append(out, player)
	}
	return out
}

func projectionCategories(rules draft.Rules) []projection.LeagueCategory {
	out := make([]projection.LeagueCategory, 0, len(rules.Categories))
	for _, c := range rules.Categories {
		out = append(out, projection.LeagueCategory{LeagueID: rules.LeagueID, StatID: c.StatID, Name: c.Label(), DisplayOnly: !c.Scores()})
	}
	return out
}

// SeasonID is the NHL season ID ("20262027") of a season start year.
func SeasonID(startYear int) int {
	return draft.NHLSeasonID(startYear)
}

func (r *Refresher) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

func (r *Refresher) heartbeat(step string) {
	if r.Heartbeat != nil {
		r.Heartbeat(step)
	}
}
