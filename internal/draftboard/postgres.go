package draftboard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// RankingReader loads the latest immutable full ranking snapshot.
type RankingReader interface {
	LatestSnapshotID(context.Context, int, int) (uuid.UUID, error)
	LoadSnapshot(context.Context, uuid.UUID) (*draftrank.Snapshot, error)
}

// NewPGService wires PostgreSQL state, immutable ranking reads, recommendation
// persistence and the process-scoped watcher controller.
func NewPGService(pool *pgxpool.Pool, rankings RankingReader, refresher Refresher,
	watchFactory func() WatchRunner, watchOptions draftwatch.WatchOptions, options Options) *Service {
	data := NewPGDataSource(pool, rankings)
	options.Watch = NewWatchController(data, watchFactory, watchOptions)
	return NewService(data, draftrecommend.NewService(draftrecommend.NewRepository(pool)), refresher, options)
}

// PGDataSource loads board inputs and shortlist state from PostgreSQL.
type PGDataSource struct {
	pool    *pgxpool.Pool
	queries *sqlcdb.Queries
	ranking RankingReader
	session *draftwatch.Repository
}

// NewPGDataSource builds the concrete data source around a shared pool.
func NewPGDataSource(pool *pgxpool.Pool, ranking RankingReader) *PGDataSource {
	return &PGDataSource{pool: pool, queries: sqlcdb.New(pool), ranking: ranking,
		session: draftwatch.NewRepository(pool)}
}

func (s *PGDataSource) ResolveIdentity(ctx context.Context, ref string, season int) (draftwatch.Identity, error) {
	return draftwatch.ResolveIdentity(ctx, s.pool, ref, season)
}

func (s *PGDataSource) Session(ctx context.Context, id draftwatch.Identity) (draftwatch.Session, error) {
	session, err := s.session.Get(ctx, id.LeagueKey)
	if errors.Is(err, draftwatch.ErrSessionNotFound) {
		return s.session.Ensure(ctx, id)
	}
	return session, err
}

func (s *PGDataSource) ApplyManual(ctx context.Context, id draftwatch.Identity, operation draftsession.ManualOperation,
	expectedVersion uint64, at time.Time) (draftwatch.Session, draftsession.Report, error) {
	return s.session.ApplyManualAtVersion(ctx, id, operation, expectedVersion, at)
}

func (s *PGDataSource) ResolveConflict(ctx context.Context, id draftwatch.Identity, key draftsession.PickKey,
	choice draftsession.ConflictChoice, expectedVersion uint64, at time.Time) (draftwatch.Session, draftsession.Report, error) {
	return s.session.ResolveConflictAtVersion(ctx, id, key, choice, expectedVersion, at)
}

func (s *PGDataSource) ListEvents(ctx context.Context, leagueKey string, afterVersion uint64, limit int) ([]draftwatch.Event, error) {
	return s.session.ListEvents(ctx, leagueKey, afterVersion, limit)
}

func (s *PGDataSource) Ranking(ctx context.Context, id draftwatch.Identity) (*draftrank.Snapshot, error) {
	latest, err := s.ranking.LatestSnapshotID(ctx, id.Season, id.LeagueID)
	if err != nil {
		return nil, err
	}
	if latest == uuid.Nil {
		return nil, fmt.Errorf("draft ranking snapshot is not available for %s", id.LeagueKey)
	}
	return s.ranking.LoadSnapshot(ctx, latest)
}

func (s *PGDataSource) LeagueData(ctx context.Context, id draftwatch.Identity) (LeagueData, error) {
	rules, err := draft.LoadSnapshot(ctx, s.queries, id.Season, id.LeagueID)
	if err != nil {
		return LeagueData{}, err
	}
	league, err := s.queries.GetYahooLeague(ctx, int32(id.LeagueID))
	if err != nil {
		return LeagueData{}, fmt.Errorf("load Yahoo league %s: %w", id.LeagueKey, err)
	}
	if int(league.Season) != id.Season || league.LeagueKey != id.LeagueKey {
		return LeagueData{}, fmt.Errorf("yahoo league row %s/%d does not match requested %s/%d",
			league.LeagueKey, league.Season, id.LeagueKey, id.Season)
	}
	team, err := s.queries.GetYahooOwnedTeam(ctx, int32(id.LeagueID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LeagueData{}, ErrNoOwnedTeam
		}
		return LeagueData{}, fmt.Errorf("load owned Yahoo team for %s: %w", id.LeagueKey, err)
	}
	roster, err := s.roster(ctx, id, int(team.ID))
	if err != nil {
		return LeagueData{}, err
	}
	data := LeagueData{Rules: rules, TeamID: int(team.ID), TeamName: team.Name,
		DraftFormat: league.DraftType, Roster: roster}
	if league.IsAuctionDraft {
		data.DraftFormat = "auction"
	}
	if team.DraftPosition.Valid && team.DraftPosition.Int32 > 0 {
		position := int(team.DraftPosition.Int32)
		data.DraftPosition = position
	}
	if league.DraftPickTime.Valid && league.DraftPickTime.Int32 > 0 {
		seconds := int(league.DraftPickTime.Int32)
		data.PickClock = &seconds
	}
	data.EstimatedOrder, err = s.estimatedDraftOrder(ctx, league, rules.Rules.RosterSlots)
	if err != nil {
		return LeagueData{}, err
	}
	return data, nil
}

func (s *PGDataSource) estimatedDraftOrder(ctx context.Context, league sqlcdb.YahooLeague,
	slots []draft.RosterSlot) ([]draftrecommend.PickSlot, error) {
	if !supportsEstimatedOrder(league.DraftType, league.IsAuctionDraft) {
		return nil, nil
	}
	rows, err := s.queries.GetYahooTeamsByLeague(ctx, league.ID)
	if err != nil {
		return nil, fmt.Errorf("load yahoo draft positions for %s: %w", league.LeagueKey, err)
	}
	teams := make([]teamDraftPosition, 0, len(rows))
	for _, row := range rows {
		position := 0
		if row.DraftPosition.Valid {
			position = int(row.DraftPosition.Int32)
		}
		teams = append(teams, teamDraftPosition{TeamID: int(row.ID), Position: position})
	}
	return estimatedSnakeOrder(int(league.NumTeams), slots, teams), nil
}

func (s *PGDataSource) roster(ctx context.Context, id draftwatch.Identity, teamID int) ([]draft.RosterPlayer, error) {
	rows, err := s.queries.GetYahooTeamRostersByTeam(ctx, sqlcdb.GetYahooTeamRostersByTeamParams{
		LeagueID: int32(id.LeagueID), TeamID: int32(teamID),
	})
	if err != nil {
		return nil, fmt.Errorf("load roster of Yahoo team %d: %w", teamID, err)
	}
	latest := latestRosterDate(rows)
	players := make([]draft.RosterPlayer, 0, len(rows))
	seen := make(map[int]bool, len(rows))
	for _, row := range rows {
		if !sameDate(row.Date, latest) || row.PlayerID <= 0 || seen[int(row.PlayerID)] {
			continue
		}
		seen[int(row.PlayerID)] = true
		players = append(players, draft.RosterPlayer{YahooPlayerID: int(row.PlayerID),
			Name:              fmt.Sprintf("Yahoo player %d", row.PlayerID),
			EligiblePositions: append([]string(nil), row.EligiblePositions...)})
	}
	return players, nil
}

func latestRosterDate(rows []sqlcdb.YahooTeamRoster) pgtype.Date {
	var latest pgtype.Date
	for _, row := range rows {
		if row.Date.Valid && (!latest.Valid || row.Date.Time.After(latest.Time)) {
			latest = row.Date
		}
	}
	return latest
}

func sameDate(left, right pgtype.Date) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Time.Equal(right.Time))
}

func (s *PGDataSource) ListShortlist(ctx context.Context, leagueKey string) ([]string, error) {
	const query = `SELECT player_key FROM draft_shortlist WHERE league_key=$1 ORDER BY created_at, player_key`
	rows, err := s.pool.Query(ctx, query, leagueKey)
	if err != nil {
		return nil, fmt.Errorf("list draft shortlist: %w", err)
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan draft shortlist: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *PGDataSource) SetShortlist(ctx context.Context, leagueKey, playerKey string, expectedVersion uint64, selected bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin draft shortlist update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const versionQuery = `SELECT state_version FROM draft_sessions WHERE league_key=$1 FOR UPDATE`
	var current int64
	if err := tx.QueryRow(ctx, versionQuery, leagueKey).Scan(&current); err != nil {
		return fmt.Errorf("read draft session version for shortlist: %w", err)
	}
	if current < 0 {
		return fmt.Errorf("draft session %s has invalid state version %d", leagueKey, current)
	}
	if uint64(current) != expectedVersion {
		return versionConflict(&draftwatch.StaleStateVersionError{Expected: expectedVersion, Actual: uint64(current)})
	}
	changed, err := updateShortlist(ctx, tx, leagueKey, playerKey, selected)
	if err != nil {
		return err
	}
	if changed {
		const bump = `UPDATE draft_sessions SET sync_version=sync_version+1, updated_at=now() WHERE league_key=$1`
		if _, err := tx.Exec(ctx, bump, leagueKey); err != nil {
			return fmt.Errorf("advance draft sync version for shortlist: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit draft shortlist update: %w", err)
	}
	return nil
}

func updateShortlist(ctx context.Context, tx pgx.Tx, leagueKey, playerKey string, selected bool) (bool, error) {
	if selected {
		const insert = `INSERT INTO draft_shortlist (league_key, player_key, created_at) VALUES ($1,$2,$3) ON CONFLICT (league_key, player_key) DO NOTHING`
		result, err := tx.Exec(ctx, insert, leagueKey, playerKey, time.Now().UTC())
		if err != nil {
			return false, fmt.Errorf("save draft shortlist player: %w", err)
		}
		return result.RowsAffected() > 0, nil
	}
	const remove = `DELETE FROM draft_shortlist WHERE league_key=$1 AND player_key=$2`
	result, err := tx.Exec(ctx, remove, leagueKey, playerKey)
	if err != nil {
		return false, fmt.Errorf("remove draft shortlist player: %w", err)
	}
	return result.RowsAffected() > 0, nil
}
