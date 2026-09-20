package graph

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	listGamesArgCount          = 7
	listGamesStartDateArgIndex = 4
	listGamesEndDateArgIndex   = 5
)

type dateRecordingDBTX struct {
	*recordingDBTX
	args []any
}

func (d *dateRecordingDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.args = append([]any(nil), args...)
	return d.recordingDBTX.Query(ctx, sql, args...)
}

func TestRequiredDateResolversRejectMalformedDatesBeforeDatabaseAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		resolve func(*Resolver) error
	}{
		{
			name: "gamesByDate date",
			resolve: func(r *Resolver) error {
				_, err := r.Query().GamesByDate(context.Background(), "not-a-date")
				return err
			},
		},
		{
			name: "standings date",
			resolve: func(r *Resolver) error {
				_, err := r.Query().Standings(context.Background(), 20242025, "not-a-date")
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := &recordingDBTX{}
			err := test.resolve(resolverWithDB(db))
			require.Error(t, err)
			assert.ErrorContains(t, err, "date")
			assert.ErrorContains(t, err, "expected YYYY-MM-DD")
			assert.Empty(t, db.recorded())
		})
	}
}

func TestGamesRejectsMalformedDateFiltersBeforeDatabaseAccess(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"startDate", "endDate"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			invalidDate := "not-a-date"
			filter := &model.GameFilter{}
			if field == "startDate" {
				filter.StartDate = &invalidDate
			} else {
				filter.EndDate = &invalidDate
			}

			db := &recordingDBTX{}
			_, err := resolverWithDB(db).Query().Games(context.Background(), filter)
			require.Error(t, err)
			assert.ErrorContains(t, err, field)
			assert.ErrorContains(t, err, "expected YYYY-MM-DD")
			assert.Empty(t, db.recorded())
		})
	}
}

func TestDateResolversAcceptValidAndOmittedDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantQuery string
		resolve   func(*Resolver) error
	}{
		{
			name:      "gamesByDate valid date",
			wantQuery: "GetGamesByDate",
			resolve: func(r *Resolver) error {
				_, err := r.Query().GamesByDate(context.Background(), "2024-03-15")
				return err
			},
		},
		{
			name:      "standings valid date",
			wantQuery: "GetStandingsSnapshotsBySeasonAndDate",
			resolve: func(r *Resolver) error {
				_, err := r.Query().Standings(context.Background(), 20232024, "2024-03-15")
				return err
			},
		},
		{
			name:      "games omitted dates",
			wantQuery: "ListGames",
			resolve: func(r *Resolver) error {
				_, err := r.Query().Games(context.Background(), &model.GameFilter{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := &recordingDBTX{}
			err := test.resolve(resolverWithDB(db))
			require.NoError(t, err)
			assert.Equal(t, []string{test.wantQuery}, db.recorded())
		})
	}
}

func TestGamesPreservesValidDateFilters(t *testing.T) {
	t.Parallel()

	startDate := "2024-03-15"
	endDate := "2024-03-20"
	db := &dateRecordingDBTX{recordingDBTX: &recordingDBTX{}}
	resolver := &Resolver{Queries: sqlcdb.New(db)}

	_, err := resolver.Query().Games(context.Background(), &model.GameFilter{
		StartDate: &startDate,
		EndDate:   &endDate,
	})
	require.NoError(t, err)
	require.Len(t, db.args, listGamesArgCount)
	assert.Equal(t, pgtype.Date{
		Time:  time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC),
		Valid: true,
	}, db.args[listGamesStartDateArgIndex])
	assert.Equal(t, pgtype.Date{
		Time:  time.Date(2024, time.March, 20, 0, 0, 0, 0, time.UTC),
		Valid: true,
	}, db.args[listGamesEndDateArgIndex])
}

// TestEscapeLikePattern pins B9: user input must be matched literally so a
// term like "%" cannot wildcard-match the whole players table.
func TestEscapeLikePattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain name is untouched", "mcdavid", "mcdavid"},
		{"percent is escaped", "%", `\%`},
		{"underscore is escaped", "a_b", `a\_b`},
		{"backslash is escaped first", `a\b`, `a\\b`},
		{"combo", `%_\`, `\%\_\\`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, escapeLikePattern(tc.in))
		})
	}
}

// TestClampListLimit pins B9: client-supplied limits above maxListLimit are
// capped, while nil / non-positive limits fall through to the query default.
func TestClampListLimit(t *testing.T) {
	t.Parallel()

	t.Run("nil passes through", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, clampListLimit(nil))
	})

	t.Run("non-positive passes through unchanged", func(t *testing.T) {
		t.Parallel()
		zero := 0
		neg := -5
		assert.Equal(t, &zero, clampListLimit(&zero))
		assert.Equal(t, &neg, clampListLimit(&neg))
	})

	t.Run("within cap is unchanged", func(t *testing.T) {
		t.Parallel()
		v := 50
		assert.Equal(t, 50, *clampListLimit(&v))
	})

	t.Run("at cap is unchanged", func(t *testing.T) {
		t.Parallel()
		v := maxListLimit
		assert.Equal(t, maxListLimit, *clampListLimit(&v))
	})

	t.Run("above cap is clamped", func(t *testing.T) {
		t.Parallel()
		v := maxListLimit + 1_000_000
		assert.Equal(t, maxListLimit, *clampListLimit(&v))
	})
}
