package yahoo

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
)

// stubTx is a Transactor that runs fn with q, counting the transactions.
type stubTx struct {
	q     YahooDataUpserter
	calls int
}

func (s *stubTx) InTx(_ context.Context, fn func(YahooDataUpserter) error) error {
	s.calls++
	return fn(s.q)
}

func TestDateRowsEmpty(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	assert.True(t, dateRows{date: day}.empty())
	assert.False(t, dateRows{date: day, summaries: summaryRows{summaries: []sqlcdb.UpsertYahooTeamSummaryBatchParams{{}}}}.empty())
	assert.False(t, dateRows{date: day, rosters: []sqlcdb.UpsertYahooTeamRosterBatchParams{{}}}.empty())
	assert.False(t, dateRows{date: day, standings: []leagueStandingRows{{leagueID: 1}}}.empty(),
		"a snapshot without configured teams still clears the league's standings")
}
