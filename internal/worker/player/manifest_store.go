package player

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
)

// pgForeignKeyViolation is the SQLSTATE of a foreign key violation: an error
// row whose manifest no longer exists, or whose player is not in it.
const pgForeignKeyViolation = "23503"

// PgProcessPlayersStore is the PostgreSQL ProcessPlayersStore.
type PgProcessPlayersStore struct {
	pool *pgxpool.Pool
}

var _ ProcessPlayersStore = (*PgProcessPlayersStore)(nil)

// NewPgProcessPlayersStore returns a store writing through pool.
func NewPgProcessPlayersStore(pool *pgxpool.Pool) *PgProcessPlayersStore {
	return &PgProcessPlayersStore{pool: pool}
}

// GetManifest implements ProcessPlayersStore.
func (s *PgProcessPlayersStore) GetManifest(ctx context.Context, id string) (ManifestRef, error) {
	row, err := sqlcdb.New(s.pool).GetProcessPlayersManifest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManifestRef{}, ErrManifestNotFound
	}
	if err != nil {
		return ManifestRef{}, fmt.Errorf("get process players manifest %s: %w", id, err)
	}
	return newManifestRef(row.ID, int(row.PlayerCount), row.PlayersHash), nil
}

// CreateManifest implements ProcessPlayersStore. Two attempts creating the
// same id at once conflict on the primary key: the second waits for the
// first's transaction, inserts nothing and returns the committed manifest.
func (s *PgProcessPlayersStore) CreateManifest(
	ctx context.Context, id, workflowID string, players []store.BoxscorePlayer, hash []byte,
) (ManifestRef, error) {
	if len(players) == 0 || len(players) > math.MaxInt32 {
		return ManifestRef{}, fmt.Errorf("create process players manifest %s: %d players", id, len(players))
	}
	created, err := s.insertManifest(ctx, id, workflowID, players, hash)
	if err != nil {
		return ManifestRef{}, err
	}
	if !created {
		return s.GetManifest(ctx, id)
	}
	return newManifestRef(id, len(players), hash), nil
}

// insertManifest inserts the header and players of a new manifest and
// applies the retention, in one transaction. It reports false, writing
// nothing, when the id already exists.
func (s *PgProcessPlayersStore) insertManifest(
	ctx context.Context, id, workflowID string, players []store.BoxscorePlayer, hash []byte,
) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin process players manifest %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlcdb.New(tx)

	_, err = q.InsertProcessPlayersManifest(ctx, sqlcdb.InsertProcessPlayersManifestParams{
		ID: id, WorkflowID: workflowID, PlayerCount: int32(len(players)), PlayersHash: hash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert process players manifest %s: %w", id, err)
	}
	rows := make([]sqlcdb.CopyProcessPlayersManifestPlayersParams, len(players))
	for i, p := range players {
		rows[i] = sqlcdb.CopyProcessPlayersManifestPlayersParams{
			ManifestID: id, Ordinal: int32(i), PlayerID: p.ID,
			FirstName: p.FirstName, LastName: p.LastName, Position: p.Position,
		}
	}
	if _, err := q.CopyProcessPlayersManifestPlayers(ctx, rows); err != nil {
		return false, fmt.Errorf("copy process players manifest %s players: %w", id, err)
	}
	if _, err := q.DeleteOldProcessPlayersManifests(ctx, ProcessPlayersManifestRetention); err != nil {
		return false, fmt.Errorf("delete old process players manifests: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit process players manifest %s: %w", id, err)
	}
	return true, nil
}

// ManifestPlayers implements ProcessPlayersStore.
func (s *PgProcessPlayersStore) ManifestPlayers(ctx context.Context, id string, from, to int) ([]store.BoxscorePlayer, error) {
	if from < 0 || from > to || to > math.MaxInt32 {
		return nil, fmt.Errorf("process players manifest %s: invalid window [%d, %d)", id, from, to)
	}
	rows, err := sqlcdb.New(s.pool).ListProcessPlayersManifestPlayers(ctx, sqlcdb.ListProcessPlayersManifestPlayersParams{
		ManifestID: id, FromOrdinal: int32(from), ToOrdinal: int32(to),
	})
	if err != nil {
		return nil, fmt.Errorf("list process players manifest %s [%d, %d): %w", id, from, to, err)
	}
	players := make([]store.BoxscorePlayer, len(rows))
	for i, r := range rows {
		players[i] = store.BoxscorePlayer{ID: r.PlayerID, FirstName: r.FirstName, LastName: r.LastName, Position: r.Position}
	}
	return players, nil
}

// RecordErrors implements ProcessPlayersStore. A (player, stage) pair repeated
// in errs keeps its last message: one statement cannot upsert a row twice.
func (s *PgProcessPlayersStore) RecordErrors(ctx context.Context, id string, errs []PlayerError) error {
	if len(errs) == 0 {
		return nil
	}
	params := sqlcdb.UpsertProcessPlayersManifestErrorsParams{ManifestID: id}
	index := make(map[PlayerError]int, len(errs))
	for _, e := range errs {
		key := PlayerError{PlayerID: e.PlayerID, Stage: e.Stage}
		if i, seen := index[key]; seen {
			params.Messages[i] = e.Message
			continue
		}
		index[key] = len(params.PlayerIds)
		params.PlayerIds = append(params.PlayerIds, e.PlayerID)
		params.Stages = append(params.Stages, e.Stage)
		params.Messages = append(params.Messages, e.Message)
	}
	err := sqlcdb.New(s.pool).UpsertProcessPlayersManifestErrors(ctx, params)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
		return s.classifyForeignKeyViolation(ctx, id, err)
	}
	if err != nil {
		return fmt.Errorf("record process players manifest %s errors: %w", id, err)
	}
	return nil
}

// classifyForeignKeyViolation tells the two causes of an error row's foreign
// key violation apart: ErrManifestNotFound when the manifest is gone, and
// ErrPlayerNotInManifest when an error names a player the manifest lacks.
func (s *PgProcessPlayersStore) classifyForeignKeyViolation(ctx context.Context, id string, violation error) error {
	_, err := s.GetManifest(ctx, id)
	if errors.Is(err, ErrManifestNotFound) {
		return ErrManifestNotFound
	}
	if err != nil {
		return fmt.Errorf("classify foreign key violation (%w) of manifest %s errors: %w", violation, id, err)
	}
	return fmt.Errorf("record process players manifest %s errors: %w: %w", id, ErrPlayerNotInManifest, violation)
}

// FinishManifest implements ProcessPlayersStore.
func (s *PgProcessPlayersStore) FinishManifest(ctx context.Context, id string) (int, error) {
	count, err := sqlcdb.New(s.pool).FinishProcessPlayersManifest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrManifestNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("finish process players manifest %s: %w", id, err)
	}
	return int(count), nil
}

// MatchedYahooIDs implements ProcessPlayersStore.
func (s *PgProcessPlayersStore) MatchedYahooIDs(ctx context.Context) ([]store.YahooPlayerID, error) {
	ids, err := sqlcdb.New(s.pool).ListMatchedYahooIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list matched yahoo ids: %w", err)
	}
	matched := make([]store.YahooPlayerID, len(ids))
	for i, id := range ids {
		matched[i] = store.YahooPlayerID(id)
	}
	return matched, nil
}
