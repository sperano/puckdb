package newsadjust

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// StoredRun is a saved adjustment: its identity, the stored snapshots and
// the full audit trail. Result.Snapshots is empty; Replay rebuilds them.
type StoredRun struct {
	ID                  uuid.UUID
	BaselineSnapshotID  uuid.UUID
	ScenarioSnapshotIDs map[Scenario]uuid.UUID
	Result              Result
}

// SaveRun stores an adjustment's scenario snapshots, inputs and audit trail
// in one transaction. Saving the same adjustment again replaces its rows,
// so a retried save is idempotent.
func (r *Repository) SaveRun(ctx context.Context, baselineID uuid.UUID, result Result) (uuid.UUID, error) {
	params, err := runParams(baselineID, result)
	if err != nil {
		return uuid.Nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin adjustment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.queries.WithTx(tx)
	runID, err := q.UpsertNewsAdjustmentRun(ctx, params)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store adjustment run: %w", err)
	}
	if err := clearRun(ctx, q, runID); err != nil {
		return uuid.Nil, err
	}
	if err := storeScenarios(ctx, q, runID, result); err != nil {
		return uuid.Nil, err
	}
	if err := storeEvents(ctx, q, runID, result); err != nil {
		return uuid.Nil, err
	}
	if err := storePlayers(ctx, q, runID, result.Players); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit adjustment run: %w", err)
	}
	return uuid.UUID(runID.Bytes), nil
}

func runParams(baselineID uuid.UUID, result Result) (sqlcdb.UpsertNewsAdjustmentRunParams, error) {
	encoded := make([][]byte, 0, 5)
	for _, value := range []any{result.Policy, result.Season, nonNil(result.Overrides), nonNil(result.Shadowed), nonNil(result.Alerts)} {
		data, err := json.Marshal(value)
		if err != nil {
			return sqlcdb.UpsertNewsAdjustmentRunParams{}, fmt.Errorf("encode adjustment run: %w", err)
		}
		encoded = append(encoded, data)
	}
	return sqlcdb.UpsertNewsAdjustmentRunParams{
		AdjustmentID: result.ID, MethodVersion: result.MethodVersion, PolicyHash: result.PolicyHash,
		Policy: encoded[0], BaselineSnapshotID: uuidValue(baselineID), BaselineSourceHash: result.BaselineHash,
		LeagueKey: result.LeagueKey, AsOf: timestamp(result.AsOf), Season: encoded[1],
		Overrides: encoded[2], ShadowedOverrides: encoded[3], CoverageWarnings: encoded[4],
	}, nil
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func clearRun(ctx context.Context, q *sqlcdb.Queries, runID pgtype.UUID) error {
	if err := q.DeleteNewsAdjustmentEvents(ctx, runID); err != nil {
		return fmt.Errorf("replace adjustment events: %w", err)
	}
	if err := q.DeleteNewsAdjustmentScenarios(ctx, runID); err != nil {
		return fmt.Errorf("replace adjustment scenarios: %w", err)
	}
	if err := q.DeleteNewsAdjustmentPlayers(ctx, runID); err != nil {
		return fmt.Errorf("replace adjustment players: %w", err)
	}
	return nil
}

func storeScenarios(ctx context.Context, q *sqlcdb.Queries, runID pgtype.UUID, result Result) error {
	for _, s := range Scenarios {
		snapshot, exists := result.Snapshots[s]
		if !exists {
			return fmt.Errorf("adjustment %s has no %s snapshot", result.ID, s)
		}
		snapshotID, err := projection.StoreSnapshotWith(ctx, q, snapshot)
		if err != nil {
			return fmt.Errorf("store %s snapshot: %w", s, err)
		}
		err = q.CreateNewsAdjustmentScenario(ctx, sqlcdb.CreateNewsAdjustmentScenarioParams{
			RunID: runID, Scenario: string(s), SnapshotID: uuidValue(snapshotID),
		})
		if err != nil {
			return fmt.Errorf("store %s scenario: %w", s, err)
		}
	}
	return nil
}

func storeEvents(ctx context.Context, q *sqlcdb.Queries, runID pgtype.UUID, result Result) error {
	decisions := make(map[string]Decision, len(result.Decisions))
	for _, d := range result.Decisions {
		decisions[d.EventID] = d
	}
	for _, e := range result.Events {
		d, exists := decisions[e.ID]
		if !exists {
			return fmt.Errorf("event %s has no decision", e.ID)
		}
		data, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("encode event %s: %w", e.ID, err)
		}
		scenarios := make([]string, len(d.Scenarios))
		for i, s := range d.Scenarios {
			scenarios[i] = string(s)
		}
		err = q.CreateNewsAdjustmentEvent(ctx, sqlcdb.CreateNewsAdjustmentEventParams{
			RunID: runID, EventID: e.ID, Version: int32(e.Version), PlayerKey: e.PlayerKey,
			IncidentID: pgtype.Int8{Int64: e.IncidentID, Valid: e.IncidentID != 0},
			Outcome:    string(d.Outcome), Reason: d.Reason, Scenarios: scenarios, Event: data,
		})
		if err != nil {
			return fmt.Errorf("store event %s: %w", e.ID, err)
		}
	}
	return nil
}

func storePlayers(ctx context.Context, q *sqlcdb.Queries, runID pgtype.UUID, players []PlayerAdjustment) error {
	for _, player := range players {
		data, err := json.Marshal(player)
		if err != nil {
			return fmt.Errorf("encode adjustment of %s: %w", player.PlayerKey, err)
		}
		err = q.CreateNewsAdjustmentPlayer(ctx, sqlcdb.CreateNewsAdjustmentPlayerParams{
			RunID: runID, PlayerKey: player.PlayerKey, Adjustment: data,
		})
		if err != nil {
			return fmt.Errorf("store adjustment of %s: %w", player.PlayerKey, err)
		}
	}
	return nil
}

// Replay rebuilds a stored adjustment from its stored baseline snapshot,
// policy, season, overrides and event versions, and checks that it
// reproduces the stored identity and every stored scenario snapshot.
func (r *Repository) Replay(ctx context.Context, runID uuid.UUID) (Result, error) {
	stored, err := r.LoadRun(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	baseline, err := r.projections.LoadSnapshot(ctx, stored.BaselineSnapshotID)
	if err != nil {
		return Result{}, err
	}
	replayed, err := Apply(stored.request(baseline))
	if err != nil {
		return Result{}, fmt.Errorf("replay adjustment %s: %w", stored.Result.ID, err)
	}
	if replayed.ID != stored.Result.ID {
		return Result{}, fmt.Errorf("replay of %s produced identity %s", stored.Result.ID, replayed.ID)
	}
	for _, s := range Scenarios {
		saved, err := r.projections.LoadSnapshot(ctx, stored.ScenarioSnapshotIDs[s])
		if err != nil {
			return Result{}, err
		}
		if !reflect.DeepEqual(comparablePlayers(saved), comparablePlayers(replayed.Snapshots[s])) {
			return Result{}, fmt.Errorf("replay of %s differs from its stored %s snapshot", stored.Result.ID, s)
		}
	}
	return replayed, nil
}

func (s StoredRun) request(baseline projection.Snapshot) Request {
	return Request{
		Baseline: baseline, Events: s.Result.Events,
		Overrides: append(slices.Clone(s.Result.Overrides), s.Result.Shadowed...),
		Policy:    s.Result.Policy, Season: s.Result.Season, AsOf: s.Result.AsOf,
		LeagueKey: s.Result.LeagueKey, CoverageWarnings: s.Result.Alerts,
	}
}

// replayedPlayer is what a replay must reproduce for each player.
type replayedPlayer struct {
	Values      map[projection.Stat]projection.Estimate
	Uncertainty float64
	TeamID      int64
}

func comparablePlayers(snapshot projection.Snapshot) map[string]replayedPlayer {
	out := make(map[string]replayedPlayer, len(snapshot.Players))
	for _, player := range snapshot.Players {
		compared := replayedPlayer{Values: player.Values, Uncertainty: player.Uncertainty}
		if player.TeamID != nil {
			compared.TeamID = *player.TeamID
		}
		out[player.PlayerKey] = compared
	}
	return out
}
