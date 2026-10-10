package cmd

import (
	"bytes"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeObservationsReportsReplayConditions(t *testing.T) {
	start := time.Date(2026, time.October, 4, 1, 0, 0, 0, time.UTC)
	observations := []draftwatch.Observation{
		{PolledAt: start.Add(90 * time.Second), ErrorClass: draftwatch.ErrorClassRateLimited},
		{PolledAt: start.Add(60 * time.Second), Duration: 3 * time.Second, Success: true, Changed: true, Authoritative: true, ParsedCount: 2},
		{PolledAt: start.Add(30 * time.Second), Duration: 2 * time.Second, Success: true, ParsedCount: 1},
		{PolledAt: start, ErrorClass: draftwatch.ErrorClassAuthentication},
	}

	got := summarizeObservations(observations)

	assert.Equal(t, 2, got.successes)
	assert.Equal(t, 1, got.changes)
	assert.Equal(t, 1, got.partial)
	assert.Equal(t, 1, got.authentication)
	assert.Equal(t, 1, got.rateLimited)
	assert.Equal(t, 2*time.Second, got.minimum)
	assert.Equal(t, 3*time.Second, got.median)
	assert.Equal(t, 3*time.Second, got.maximum)
	assert.Equal(t, 30*time.Second, got.maxChangeGap)
}

func TestRootExposesDraftSessionAndSyncDraft(t *testing.T) {
	root := Root()
	for _, path := range [][]string{{"sync", "draft"}, {"draft", "session", "add"}, {"draft", "session", "resolve"}} {
		command, _, err := root.Find(path)
		require.NoError(t, err)
		assert.Equal(t, path[len(path)-1], command.Name())
	}
}

func TestWriteDraftSyncOutcomeReportsSkippedPickReasons(t *testing.T) {
	var out bytes.Buffer
	outcome := draftwatch.Outcome{
		Session: draftwatch.Session{Identity: testDraftCLIIdentity()},
		Report: draftsession.Report{Skipped: []draftsession.SkippedPick{
			{Index: 1, Key: draftsession.PickKey{Round: 2, Pick: 13}, Reason: "unmapped player key"},
		}},
	}

	require.NoError(t, writeDraftSyncOutcome(&out, outcome))
	assert.Contains(t, out.String(), "skipped=1")
	assert.Contains(t, out.String(), "response_index=2 round=2 pick=13 reason=unmapped player key")
}

func testDraftCLIIdentity() draftwatch.Identity {
	return draftwatch.Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}
}
