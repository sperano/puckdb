package draftrecommend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRunNotFound = errors.New("draft recommendation run not found")

const maxDatabaseVersion = uint64(^uint64(0) >> 1)

// StoredRun is one replayable recommendation input and its persisted result.
type StoredRun struct {
	ID     uuid.UUID `json:"id"`
	Input  Input     `json:"input"`
	Result Result    `json:"result"`
}

// Repository stores complete recommendation inputs and outputs for audit and
// deterministic replay. It does not regenerate or mutate a recommendation.
type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Save persists one generated result. Metadata columns are derived from the
// frozen input and checked against the result before the insert.
func (r *Repository) Save(ctx context.Context, input Input, result Result) (uuid.UUID, error) {
	if err := validateStoredRun(input, result); err != nil {
		return uuid.Nil, err
	}
	version, err := databaseVersion(input.Session.Version)
	if err != nil {
		return uuid.Nil, err
	}
	inputRaw, err := json.Marshal(input)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode recommendation input: %w", err)
	}
	strategyRaw, err := json.Marshal(input.Strategy)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode recommendation strategy: %w", err)
	}
	resultRaw, err := json.Marshal(result)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode recommendation result: %w", err)
	}
	const insert = `
INSERT INTO draft_recommendation_runs (
    league_key, session_version, ranking_snapshot_id, ranking_identity, ranking_version,
    projection_snapshot_id, projection_version, rules_hash, scenario,
    strategy, input, result, generated_at, latency_milliseconds
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
RETURNING id`
	var id uuid.UUID
	snapshot := input.Ranking
	err = r.pool.QueryRow(ctx, insert,
		snapshot.League.LeagueKey, version, snapshot.ID,
		snapshot.Identity, result.RankingVersion, snapshot.Projection.SnapshotID, snapshot.Projection.ModelVersion,
		snapshot.League.RulesHash, result.Scenario, strategyRaw, inputRaw, resultRaw,
		result.GeneratedAt, result.LatencyMillis,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store draft recommendation: %w", err)
	}
	return id, nil
}

// Get loads one complete run for audit or replay.
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (StoredRun, error) {
	const query = `SELECT input, result FROM draft_recommendation_runs WHERE id=$1`
	var inputRaw, resultRaw []byte
	if err := r.pool.QueryRow(ctx, query, id).Scan(&inputRaw, &resultRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StoredRun{}, fmt.Errorf("%w: %s", ErrRunNotFound, id)
		}
		return StoredRun{}, fmt.Errorf("load draft recommendation %s: %w", id, err)
	}
	return decodeStoredRun(id, inputRaw, resultRaw)
}

// Latest loads the newest result generated for an exact session version.
func (r *Repository) Latest(ctx context.Context, leagueKey string, sessionVersion uint64) (StoredRun, error) {
	const query = `
SELECT id, input, result FROM draft_recommendation_runs
WHERE league_key=$1 AND session_version=$2
ORDER BY generated_at DESC, id DESC LIMIT 1`
	var id uuid.UUID
	var inputRaw, resultRaw []byte
	version, err := databaseVersion(sessionVersion)
	if err != nil {
		return StoredRun{}, err
	}
	err = r.pool.QueryRow(ctx, query, leagueKey, version).Scan(&id, &inputRaw, &resultRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StoredRun{}, fmt.Errorf("%w: league %s session %d", ErrRunNotFound, leagueKey, sessionVersion)
		}
		return StoredRun{}, fmt.Errorf("load latest draft recommendation: %w", err)
	}
	return decodeStoredRun(id, inputRaw, resultRaw)
}

func validateStoredRun(input Input, result Result) error {
	if input.Ranking == nil {
		return errors.New("store draft recommendation: ranking snapshot is required")
	}
	if result.RecommendationVersion != RecommendationVersion {
		return fmt.Errorf("store draft recommendation: unsupported version %q", result.RecommendationVersion)
	}
	if result.SessionVersion != input.Session.Version {
		return errors.New("store draft recommendation: session version mismatch")
	}
	if _, err := databaseVersion(input.Session.Version); err != nil {
		return err
	}
	if result.RankingSnapshotID != input.Ranking.ID.String() || result.RankingIdentity != input.Ranking.Identity ||
		result.RankingVersion != input.Ranking.Versions[result.Scenario] {
		return errors.New("store draft recommendation: ranking version mismatch")
	}
	if result.ProjectionVersion != input.Ranking.Projection.ModelVersion || result.RulesVersion != input.Ranking.League.RulesHash {
		return errors.New("store draft recommendation: source version mismatch")
	}
	resolvedScenario, _ := selectedScenario(input)
	if result.Scenario != resolvedScenario || result.GeneratedAt.IsZero() || result.LatencyMillis < 0 {
		return errors.New("store draft recommendation: invalid scenario, time, or latency")
	}
	return nil
}

func decodeStoredRun(id uuid.UUID, inputRaw, resultRaw []byte) (StoredRun, error) {
	run := StoredRun{ID: id}
	if err := json.Unmarshal(inputRaw, &run.Input); err != nil {
		return StoredRun{}, fmt.Errorf("decode recommendation input %s: %w", id, err)
	}
	if err := json.Unmarshal(resultRaw, &run.Result); err != nil {
		return StoredRun{}, fmt.Errorf("decode recommendation result %s: %w", id, err)
	}
	if err := validateStoredRun(run.Input, run.Result); err != nil {
		return StoredRun{}, fmt.Errorf("decode recommendation %s: %w", id, err)
	}
	return run, nil
}

func databaseVersion(version uint64) (int64, error) {
	if version > maxDatabaseVersion {
		return 0, errors.New("draft recommendation session version exceeds PostgreSQL bigint")
	}
	return int64(version), nil
}
