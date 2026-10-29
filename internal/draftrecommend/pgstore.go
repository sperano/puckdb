package draftrecommend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ErrRunNotFound is returned when no stored run matches the requested ID or
// league session version.
var ErrRunNotFound = errors.New("draft recommendation run not found")

// StoredRun is one replayable recommendation input and its persisted result.
type StoredRun struct {
	ID     uuid.UUID `json:"id"`
	Input  Input     `json:"input"`
	Result Result    `json:"result"`
}

// PGStore stores complete recommendation inputs and outputs for audit and
// deterministic replay. It does not regenerate or mutate a recommendation.
type PGStore struct{ queries *sqlcdb.Queries }

// NewPGStore returns a PGStore on pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{queries: sqlcdb.New(pool)} }

// Save persists one generated result. Metadata columns are derived from the
// frozen input and checked against the result before the insert.
func (r *PGStore) Save(ctx context.Context, input Input, result Result) (uuid.UUID, error) {
	if err := validateStoredRun(input, result); err != nil {
		return uuid.Nil, err
	}
	version, err := draftsession.DatabaseVersion(input.Session.Version)
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
	snapshot := input.Ranking
	id, err := r.queries.InsertDraftRecommendationRun(ctx, sqlcdb.InsertDraftRecommendationRunParams{
		LeagueKey: snapshot.League.LeagueKey, SessionVersion: version,
		RankingSnapshotID: uuidValue(snapshot.ID), RankingIdentity: snapshot.Identity,
		RankingVersion: result.RankingVersion, ProjectionSnapshotID: uuidValue(snapshot.Projection.SnapshotID),
		ProjectionVersion: snapshot.Projection.ModelVersion, RulesHash: snapshot.League.RulesHash,
		Scenario: string(result.Scenario), Strategy: strategyRaw, Input: inputRaw, Result: resultRaw,
		GeneratedAt:         pgtype.Timestamptz{Time: result.GeneratedAt, Valid: true},
		LatencyMilliseconds: result.LatencyMillis,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("store draft recommendation: %w", err)
	}
	return uuid.UUID(id.Bytes), nil
}

// Get loads one complete run for audit or replay.
func (r *PGStore) Get(ctx context.Context, id uuid.UUID) (StoredRun, error) {
	row, err := r.queries.GetDraftRecommendationRun(ctx, uuidValue(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StoredRun{}, fmt.Errorf("%w: %s", ErrRunNotFound, id)
		}
		return StoredRun{}, fmt.Errorf("load draft recommendation %s: %w", id, err)
	}
	return decodeStoredRun(id, row.Input, row.Result)
}

// Latest loads the newest result generated for an exact session version.
func (r *PGStore) Latest(ctx context.Context, leagueKey string, sessionVersion uint64) (StoredRun, error) {
	version, err := draftsession.DatabaseVersion(sessionVersion)
	if err != nil {
		return StoredRun{}, err
	}
	row, err := r.queries.GetLatestDraftRecommendationRun(ctx, sqlcdb.GetLatestDraftRecommendationRunParams{
		LeagueKey: leagueKey, SessionVersion: version,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StoredRun{}, fmt.Errorf("%w: league %s session %d", ErrRunNotFound, leagueKey, sessionVersion)
		}
		return StoredRun{}, fmt.Errorf("load latest draft recommendation: %w", err)
	}
	return decodeStoredRun(uuid.UUID(row.ID.Bytes), row.Input, row.Result)
}

func validateStoredRun(input Input, result Result) error {
	if err := validateInput(input); err != nil {
		return fmt.Errorf("store draft recommendation: %w", err)
	}
	if result.RecommendationVersion != RecommendationVersion {
		return fmt.Errorf("store draft recommendation: unsupported version %q", result.RecommendationVersion)
	}
	if result.SessionVersion != input.Session.Version {
		return errors.New("store draft recommendation: session version mismatch")
	}
	if _, err := draftsession.DatabaseVersion(input.Session.Version); err != nil {
		return err
	}
	if result.RankingSnapshotID != input.Ranking.ID.String() || result.RankingIdentity != input.Ranking.Identity ||
		result.RankingVersion != input.Ranking.Versions[result.Scenario] {
		return errors.New("store draft recommendation: ranking version mismatch")
	}
	if result.ProjectionVersion != input.Ranking.Projection.ModelVersion || result.RulesVersion != input.Ranking.League.RulesHash {
		return errors.New("store draft recommendation: source version mismatch")
	}
	resolvedScenario, _ := input.Ranking.ResolveScenario(input.Scenario)
	if result.Scenario != resolvedScenario || !result.GeneratedAt.Equal(input.GeneratedAt) || result.LatencyMillis < 0 ||
		!equalStrategy(result.Strategy, input.Strategy) || !equalAvailability(result.Availability, input.Availability) ||
		!equalADP(result.ADP, input.ADP) {
		return errors.New("store draft recommendation: invalid scenario, time, or latency")
	}
	return nil
}

func equalStrategy(left, right Strategy) bool {
	return left.RiskTolerance == right.RiskTolerance && left.RiskToleranceSet == right.RiskToleranceSet &&
		maps.Equal(left.CategoryWeights, right.CategoryWeights)
}

func equalAvailability(left, right AvailabilitySource) bool {
	return left.Name == right.Name && left.Version == right.Version && left.AsOf.Equal(right.AsOf) &&
		maps.Equal(left.Unavailable, right.Unavailable) && maps.Equal(left.UnavailableReasons, right.UnavailableReasons) &&
		maps.Equal(left.UnavailablePlayerIDs, right.UnavailablePlayerIDs)
}

func equalADP(left, right ADPSource) bool {
	return left.Name == right.Name && left.Version == right.Version && left.AsOf.Equal(right.AsOf) &&
		maps.Equal(left.ByPlayer, right.ByPlayer)
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

// uuidValue passes id as given, uuid.Nil included: every UUID these queries
// write or match is NOT NULL.
func uuidValue(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
