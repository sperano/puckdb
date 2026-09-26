package newsadjust

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// LoadRun reads a stored adjustment and its audit trail.
func (r *Repository) LoadRun(ctx context.Context, runID uuid.UUID) (StoredRun, error) {
	id := uuidValue(runID)
	row, err := r.queries.GetNewsAdjustmentRun(ctx, id)
	if err != nil {
		return StoredRun{}, fmt.Errorf("load adjustment run %s: %w", runID, err)
	}
	stored, err := storedRunFromRow(row)
	if err != nil {
		return StoredRun{}, err
	}
	events, err := r.queries.ListNewsAdjustmentEvents(ctx, id)
	if err != nil {
		return StoredRun{}, fmt.Errorf("load adjustment events: %w", err)
	}
	if err := stored.addEvents(events); err != nil {
		return StoredRun{}, err
	}
	scenarios, err := r.queries.ListNewsAdjustmentScenarios(ctx, id)
	if err != nil {
		return StoredRun{}, fmt.Errorf("load adjustment scenarios: %w", err)
	}
	for _, scenario := range scenarios {
		stored.ScenarioSnapshotIDs[Scenario(scenario.Scenario)] = uuid.UUID(scenario.SnapshotID.Bytes)
	}
	players, err := r.queries.ListNewsAdjustmentPlayers(ctx, id)
	if err != nil {
		return StoredRun{}, fmt.Errorf("load adjusted players: %w", err)
	}
	for _, player := range players {
		var adjustment PlayerAdjustment
		if err := json.Unmarshal(player.Adjustment, &adjustment); err != nil {
			return StoredRun{}, fmt.Errorf("decode adjustment of %s: %w", player.PlayerKey, err)
		}
		stored.Result.Players = append(stored.Result.Players, adjustment)
	}
	return stored, nil
}

func storedRunFromRow(row sqlcdb.NewsAdjustmentRun) (StoredRun, error) {
	stored := StoredRun{
		ID: uuid.UUID(row.ID.Bytes), BaselineSnapshotID: uuid.UUID(row.BaselineSnapshotID.Bytes),
		ScenarioSnapshotIDs: make(map[Scenario]uuid.UUID, len(Scenarios)),
		Result: Result{
			ID: row.AdjustmentID, MethodVersion: row.MethodVersion, PolicyHash: row.PolicyHash,
			BaselineHash: row.BaselineSourceHash, AsOf: timeOf(row.AsOf), LeagueKey: row.LeagueKey,
		},
	}
	fields := []struct {
		data   []byte
		target any
	}{
		{row.Policy, &stored.Result.Policy}, {row.Season, &stored.Result.Season},
		{row.Overrides, &stored.Result.Overrides}, {row.ShadowedOverrides, &stored.Result.Shadowed},
		{row.CoverageWarnings, &stored.Result.Alerts},
	}
	for _, field := range fields {
		if err := json.Unmarshal(field.data, field.target); err != nil {
			return StoredRun{}, fmt.Errorf("decode adjustment run %s: %w", row.AdjustmentID, err)
		}
	}
	if stored.Result.Policy.Hash() != row.PolicyHash {
		return StoredRun{}, fmt.Errorf("adjustment run %s policy no longer matches its hash", row.AdjustmentID)
	}
	return stored, nil
}

func (s *StoredRun) addEvents(rows []sqlcdb.NewsAdjustmentEvent) error {
	for _, row := range rows {
		var e Event
		if err := json.Unmarshal(row.Event, &e); err != nil {
			return fmt.Errorf("decode event %s: %w", row.EventID, err)
		}
		scenarios := make([]Scenario, len(row.Scenarios))
		for i, scenario := range row.Scenarios {
			scenarios[i] = Scenario(scenario)
		}
		if len(scenarios) == 0 {
			scenarios = nil
		}
		s.Result.Events = append(s.Result.Events, e)
		s.Result.Decisions = append(s.Result.Decisions, Decision{
			EventID: row.EventID, Version: int(row.Version), PlayerKey: row.PlayerKey, IncidentID: row.IncidentID.Int64,
			Outcome: Outcome(row.Outcome), Reason: row.Reason, Scenarios: scenarios,
		})
	}
	return nil
}
