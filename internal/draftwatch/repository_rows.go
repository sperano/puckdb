package draftwatch

import (
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// The mappings between the sqlc rows and parameters of the draft session
// queries and this package's types. Every narrowing to a PostgreSQL
// integer is checked (int4); versions go through draftsession.DatabaseVersion.

// int4 narrows value to a PostgreSQL integer, failing instead of wrapping
// when it does not fit.
func int4(name string, value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("draft session %s %d does not fit a PostgreSQL integer", name, value)
	}
	return int32(value), nil
}

// timestamptz passes t as given, the zero time included: every timestamp
// these queries write is NOT NULL.
func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// optionalTime is a nullable timestamp column as a pointer, nil for NULL.
func optionalTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time
	return &value
}

func ensureSessionParams(id Identity) (sqlcdb.EnsureDraftSessionParams, error) {
	season, err := int4("season", id.Season)
	if err != nil {
		return sqlcdb.EnsureDraftSessionParams{}, err
	}
	leagueID, err := int4("league ID", id.LeagueID)
	if err != nil {
		return sqlcdb.EnsureDraftSessionParams{}, err
	}
	gameKey, err := int4("game key", id.GameKey)
	if err != nil {
		return sqlcdb.EnsureDraftSessionParams{}, err
	}
	return sqlcdb.EnsureDraftSessionParams{LeagueKey: id.LeagueKey, Season: season,
		LeagueID: leagueID, GameKey: gameKey}, nil
}

func sessionFromRow(leagueKey string, row sqlcdb.GetDraftSessionRow) (Session, error) {
	state, err := decodeState(row.Board)
	if err != nil {
		return Session{}, err
	}
	if row.StateVersion < 0 || state.Version != uint64(row.StateVersion) {
		return Session{}, fmt.Errorf("draft session %s version mismatch: row=%d board=%d",
			leagueKey, row.StateVersion, state.Version)
	}
	if row.SyncVersion < 0 {
		return Session{}, fmt.Errorf("draft session %s has invalid sync version %d", leagueKey, row.SyncVersion)
	}
	return Session{
		Identity: Identity{LeagueKey: leagueKey, Season: int(row.Season), LeagueID: int(row.LeagueID),
			GameKey: int(row.GameKey)},
		State:               state,
		DraftStatus:         row.DraftStatus,
		RecommendationsSafe: row.RecommendationsSafe,
		Complete:            row.Complete,
		UpstreamPickCount:   int(row.UpstreamPickCount),
		SkippedPickCount:    int(row.SkippedPickCount),
		LastPollAt:          optionalTime(row.LastPollAt),
		LastSuccessAt:       optionalTime(row.LastSuccessAt),
		LastAuthoritativeAt: optionalTime(row.LastAuthoritativeAt),
		LastError:           row.LastError,
		SyncVersion:         uint64(row.SyncVersion),
		UpdatedAt:           row.UpdatedAt.Time,
	}, nil
}

func eventFromRow(row sqlcdb.ListDraftSessionEventsRow) (Event, error) {
	if row.StateVersion <= 0 {
		return Event{}, fmt.Errorf("draft session event has invalid version %d", row.StateVersion)
	}
	return Event{StateVersion: uint64(row.StateVersion), Kind: row.Kind, Details: row.Details,
		CreatedAt: row.CreatedAt.Time}, nil
}

func observationFromRow(row sqlcdb.ListDraftSessionObservationsRow) Observation {
	return Observation{
		PolledAt:      row.PolledAt.Time,
		Duration:      time.Duration(row.DurationMs) * time.Millisecond,
		Success:       row.Success,
		Authoritative: row.Authoritative,
		Changed:       row.Changed,
		DraftStatus:   row.DraftStatus,
		DeclaredCount: int(row.DeclaredCount),
		ParsedCount:   int(row.ParsedCount),
		SkippedCount:  int(row.SkippedCount),
		SnapshotHash:  row.SnapshotHash,
		ErrorClass:    ErrorClass(row.ErrorClass),
		Error:         row.Error,
	}
}

func observationParams(leagueKey string, observation Observation) (sqlcdb.InsertDraftSessionObservationParams, error) {
	declared, err := int4("declared count", observation.DeclaredCount)
	if err != nil {
		return sqlcdb.InsertDraftSessionObservationParams{}, err
	}
	parsed, err := int4("parsed count", observation.ParsedCount)
	if err != nil {
		return sqlcdb.InsertDraftSessionObservationParams{}, err
	}
	skipped, err := int4("skipped count", observation.SkippedCount)
	if err != nil {
		return sqlcdb.InsertDraftSessionObservationParams{}, err
	}
	return sqlcdb.InsertDraftSessionObservationParams{
		LeagueKey: leagueKey, PolledAt: timestamptz(observation.PolledAt),
		DurationMs: observation.Duration.Milliseconds(), Success: observation.Success,
		Authoritative: observation.Authoritative, Changed: observation.Changed,
		DraftStatus: observation.DraftStatus, DeclaredCount: declared, ParsedCount: parsed,
		SkippedCount: skipped, SnapshotHash: observation.SnapshotHash,
		ErrorClass: string(observation.ErrorClass), Error: observation.Error,
	}, nil
}
