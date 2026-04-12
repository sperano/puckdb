package graph

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
)

// pgtype → Go conversion helpers for GraphQL resolvers.

func ptrInt(v int) *int       { return &v }
func ptrInt64(v int64) *int64 { return &v }
func ptrFloat64(v float64) *float64 { return &v }
func ptrBool(v bool) *bool    { return &v }

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func int4Ptr(i pgtype.Int4) *int {
	if !i.Valid {
		return nil
	}
	v := int(i.Int32)
	return &v
}

func int8Ptr(i pgtype.Int8) *int64 {
	if !i.Valid {
		return nil
	}
	return &i.Int64
}

func float4Ptr(f pgtype.Float4) *float64 {
	if !f.Valid {
		return nil
	}
	v := float64(f.Float32)
	return &v
}

func boolPtr(b pgtype.Bool) *bool {
	if !b.Valid {
		return nil
	}
	return &b.Bool
}

func dateString(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format(time.DateOnly)
}

func dateStringPtr(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format(time.DateOnly)
	return &s
}

func timestampPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func nullPositionPtr(p sqlcdb.NullPlayerPosition) *string {
	if !p.Valid {
		return nil
	}
	s := string(p.PlayerPosition)
	return &s
}

func nullHandSidePtr(h sqlcdb.NullHandSide) *string {
	if !h.Valid {
		return nil
	}
	s := string(h.HandSide)
	return &s
}

func nullGoalieDecisionPtr(d sqlcdb.NullGoalieDecision) *string {
	if !d.Valid {
		return nil
	}
	s := string(d.GoalieDecision)
	return &s
}

func parseDate(s string) pgtype.Date {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

func optionalInt4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

func optionalInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func optionalText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}

func optionalBool(v *bool) pgtype.Bool {
	if v == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *v, Valid: true}
}

func optionalDate(v *string) pgtype.Date {
	if v == nil {
		return pgtype.Date{}
	}
	return parseDate(*v)
}

func optionalGameType(v *string) sqlcdb.NullGameType {
	if v == nil {
		return sqlcdb.NullGameType{}
	}
	return sqlcdb.NullGameType{GameType: sqlcdb.GameType(*v), Valid: true}
}

func optionalGameState(v *string) sqlcdb.NullGameState {
	if v == nil {
		return sqlcdb.NullGameState{}
	}
	return sqlcdb.NullGameState{GameState: sqlcdb.GameState(*v), Valid: true}
}
