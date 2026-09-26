package draftrank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ErrSnapshotNotFound means no stored snapshot has the requested ID.
var ErrSnapshotNotFound = errors.New("ranking snapshot not found")

// PGStore keeps snapshots and refresh attempts in PostgreSQL.
type PGStore struct {
	pool    *pgxpool.Pool
	queries *sqlcdb.Queries
}

// NewPGStore returns a store over the pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool, queries: sqlcdb.New(pool)}
}

// SaveSnapshot stores a snapshot with its players in one transaction and
// keeps only the newest keep snapshots of the league (all when keep <= 0).
// Writes of one league are serialized, and a snapshot only becomes visible
// once committed, so readers see either the previous snapshot or the new
// one, never a partial one.
func (s *PGStore) SaveSnapshot(ctx context.Context, snapshot Snapshot, keep int) (uuid.UUID, error) {
	meta, err := json.Marshal(snapshot.Meta)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode snapshot meta: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin snapshot transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.queries.WithTx(tx)
	league := snapshot.League
	if err := q.LockDraftRankingLeague(ctx, sqlcdb.LockDraftRankingLeagueParams{Season: int32(league.Season), LeagueID: int32(league.LeagueID)}); err != nil {
		return uuid.Nil, fmt.Errorf("lock league %s: %w", league.LeagueKey, err)
	}
	id, err := q.CreateDraftRankingSnapshot(ctx, snapshotParams(snapshot, meta))
	if err != nil {
		return uuid.Nil, fmt.Errorf("store snapshot of %s: %w", league.LeagueKey, err)
	}
	if err := storePlayers(ctx, q, id, snapshot.Players); err != nil {
		return uuid.Nil, err
	}
	if keep > 0 {
		if _, err := q.PruneDraftRankingSnapshots(ctx, sqlcdb.PruneDraftRankingSnapshotsParams{
			Season: int32(league.Season), LeagueID: int32(league.LeagueID), Keep: int32(keep),
		}); err != nil {
			return uuid.Nil, fmt.Errorf("prune snapshots of %s: %w", league.LeagueKey, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit snapshot of %s: %w", league.LeagueKey, err)
	}
	return uuid.UUID(id.Bytes), nil
}

func snapshotParams(snapshot Snapshot, meta []byte) sqlcdb.CreateDraftRankingSnapshotParams {
	params := sqlcdb.CreateDraftRankingSnapshotParams{
		Season: int32(snapshot.League.Season), LeagueID: int32(snapshot.League.LeagueID),
		LeagueKey: snapshot.League.LeagueKey, Identity: snapshot.Identity, RulesHash: snapshot.League.RulesHash,
		ProjectionSnapshotID: uuidValue(snapshot.Projection.SnapshotID), AsOf: timestamp(snapshot.AsOf), Meta: meta,
	}
	if snapshot.Adjustment != nil {
		params.AdjustmentRunID = uuidValue(snapshot.Adjustment.RunID)
	}
	return params
}

func storePlayers(ctx context.Context, q *sqlcdb.Queries, id pgtype.UUID, players []Player) error {
	for _, player := range players {
		data, err := json.Marshal(player)
		if err != nil {
			return fmt.Errorf("encode player %s: %w", player.PlayerKey, err)
		}
		if err := q.CreateDraftRankingPlayer(ctx, sqlcdb.CreateDraftRankingPlayerParams{
			SnapshotID: id, PlayerKey: player.PlayerKey,
			BaselineRank: int32(player.Placements[ScenarioBaseline].OverallRank), Player: data,
		}); err != nil {
			return fmt.Errorf("store player %s: %w", player.PlayerKey, err)
		}
	}
	return nil
}

// LoadSnapshot reads a stored snapshot with its players.
func (s *PGStore) LoadSnapshot(ctx context.Context, id uuid.UUID) (*Snapshot, error) {
	snapshot, err := s.SnapshotInfo(ctx, id)
	if err != nil {
		return nil, err
	}
	players, err := s.queries.ListDraftRankingPlayers(ctx, uuidValue(id))
	if err != nil {
		return nil, fmt.Errorf("load players of snapshot %s: %w", id, err)
	}
	full := &Snapshot{SnapshotInfo: *snapshot, Players: make([]Player, 0, len(players))}
	for _, p := range players {
		var player Player
		if err := json.Unmarshal(p.Player, &player); err != nil {
			return nil, fmt.Errorf("decode player %s of snapshot %s: %w", p.PlayerKey, id, err)
		}
		full.Players = append(full.Players, player)
	}
	return full, nil
}

// SnapshotInfo reads a stored snapshot without its players.
func (s *PGStore) SnapshotInfo(ctx context.Context, id uuid.UUID) (*SnapshotInfo, error) {
	row, err := s.queries.GetDraftRankingSnapshot(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrSnapshotNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("load snapshot %s: %w", id, err)
	}
	info := &SnapshotInfo{
		ID: uuid.UUID(row.ID.Bytes), Identity: row.Identity,
		AsOf: row.AsOf.Time.UTC(), CreatedAt: row.CreatedAt.Time.UTC(),
	}
	if err := json.Unmarshal(row.Meta, &info.Meta); err != nil {
		return nil, fmt.Errorf("decode snapshot %s: %w", id, err)
	}
	return info, nil
}

// LatestSnapshotID returns the league's newest snapshot, uuid.Nil if none.
func (s *PGStore) LatestSnapshotID(ctx context.Context, season, leagueID int) (uuid.UUID, error) {
	row, err := s.queries.GetLatestDraftRankingSnapshot(ctx, sqlcdb.GetLatestDraftRankingSnapshotParams{
		Season: int32(season), LeagueID: int32(leagueID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("find latest snapshot of season %d league %d: %w", season, leagueID, err)
	}
	return uuid.UUID(row.ID.Bytes), nil
}

// StartRefresh records a running attempt; a retried activity of the same
// run reuses its row.
func (s *PGStore) StartRefresh(ctx context.Context, runID string, season, leagueID int, at time.Time) (uuid.UUID, error) {
	id, err := s.queries.StartDraftRankingRefresh(ctx, sqlcdb.StartDraftRankingRefreshParams{
		RunID: runID, Season: int32(season), LeagueID: int32(leagueID), StartedAt: timestamp(at),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("record refresh of season %d league %d: %w", season, leagueID, err)
	}
	return uuid.UUID(id.Bytes), nil
}

// FinishRefresh records an attempt's outcome.
func (s *PGStore) FinishRefresh(ctx context.Context, id uuid.UUID, outcome Refresh, leagueKey string) error {
	params := sqlcdb.FinishDraftRankingRefreshParams{
		ID: uuidValue(id), Status: string(outcome.State), State: string(outcome.Code), Error: outcome.Error,
		LeagueKey: leagueKey, FinishedAt: timestamp(outcome.FinishedAt),
	}
	if outcome.SnapshotID != uuid.Nil {
		params.SnapshotID = uuidValue(outcome.SnapshotID)
	}
	if err := s.queries.FinishDraftRankingRefresh(ctx, params); err != nil {
		return fmt.Errorf("record refresh outcome %s: %w", id, err)
	}
	return nil
}

// CancelRefreshes marks a run's unfinished attempts canceled.
func (s *PGStore) CancelRefreshes(ctx context.Context, runID string, at time.Time) (int64, error) {
	n, err := s.queries.CancelDraftRankingRefreshes(ctx, sqlcdb.CancelDraftRankingRefreshesParams{RunID: runID, FinishedAt: timestamp(at)})
	if err != nil {
		return 0, fmt.Errorf("cancel refreshes of run %s: %w", runID, err)
	}
	return n, nil
}

// LatestRefresh returns the league's newest refresh attempt, nil if none.
func (s *PGStore) LatestRefresh(ctx context.Context, season, leagueID int) (*Refresh, error) {
	row, err := s.queries.GetLatestDraftRankingRefresh(ctx, sqlcdb.GetLatestDraftRankingRefreshParams{
		Season: int32(season), LeagueID: int32(leagueID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find latest refresh of season %d league %d: %w", season, leagueID, err)
	}
	refresh := &Refresh{
		ID: uuid.UUID(row.ID.Bytes), RunID: row.RunID, State: RefreshState(row.Status), Code: IssueCode(row.State),
		Error: row.Error, StartedAt: row.StartedAt.Time.UTC(),
	}
	if row.SnapshotID.Valid {
		refresh.SnapshotID = uuid.UUID(row.SnapshotID.Bytes)
	}
	if row.FinishedAt.Valid {
		refresh.FinishedAt = row.FinishedAt.Time.UTC()
	}
	return refresh, nil
}

// LeagueRules returns a league's latest rules, nil when none were imported.
func (s *PGStore) LeagueRules(ctx context.Context, season, leagueID int) (*draft.Snapshot, error) {
	rules, err := draft.LoadSnapshot(ctx, s.queries, season, leagueID)
	if errors.Is(err, draft.ErrNoRules) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rules, nil
}

// ListLeagueRules returns the latest rules of every league of a season.
func (s *PGStore) ListLeagueRules(ctx context.Context, season int) ([]draft.Snapshot, error) {
	rows, err := s.queries.ListLatestYahooLeagueRuleSnapshots(ctx, int32(season))
	if err != nil {
		return nil, fmt.Errorf("list league rules of season %d: %w", season, err)
	}
	out := make([]draft.Snapshot, 0, len(rows))
	for _, row := range rows {
		rules, err := draft.SnapshotFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, rules)
	}
	return out, nil
}

// FindLeagueKey returns the season and numeric ID of a league key.
func (s *PGStore) FindLeagueKey(ctx context.Context, leagueKey string) (int, int, error) {
	row, err := s.queries.FindYahooLeagueRuleSnapshotByKey(ctx, leagueKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, fmt.Errorf("%w: league %s has no imported rules", ErrUnknownLeague, leagueKey)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("resolve league %s: %w", leagueKey, err)
	}
	return int(row.Season), int(row.LeagueID), nil
}

// OverrideChanges counts overrides of the league created, reset or expired
// in (since, now].
func (s *PGStore) OverrideChanges(ctx context.Context, leagueKey string, since, now time.Time) (int64, error) {
	n, err := s.queries.CountNewsAdjustmentOverrideChanges(ctx, sqlcdb.CountNewsAdjustmentOverrideChangesParams{
		LeagueKey: leagueKey, Since: timestamp(since), Now: timestamp(now),
	})
	if err != nil {
		return 0, fmt.Errorf("count override changes of %s: %w", leagueKey, err)
	}
	return n, nil
}

func uuidValue(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil}
}

func timestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: !t.IsZero()}
}
