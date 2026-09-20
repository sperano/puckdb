package nhl

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- workflowIDExtractSeason ---

func TestWorkflowIDExtractSeason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		startYear int
		expected  string
	}{
		{2023, "extract-season-2023"},
		{2024, "extract-season-2024"},
		{0, "extract-season-0"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, workflowIDExtractSeason(tt.startYear))
		})
	}
}

// --- addSkaterPlayer ---

func TestAddSkaterPlayer(t *testing.T) {
	t.Parallel()

	players := make(map[int64]shared.PartialPlayer)
	skater := nhlapi.SkaterStats{
		PlayerID:      nhlapi.PlayerID(8478402),
		Name:          nhlapi.LocalizedString{Default: "Connor McDavid"},
		Position:      "C",
		SweaterNumber: 97,
	}

	addSkaterPlayer(players, skater, 22)

	require.Len(t, players, 1)
	p := players[8478402]
	assert.Equal(t, int64(8478402), p.ID)
	assert.Equal(t, "Connor", p.FirstName)
	assert.Equal(t, "McDavid", p.LastName)
	assert.Equal(t, "C", p.Position)
	assert.Equal(t, 97, p.SweaterNumber)
	assert.Equal(t, int64(22), p.TeamID)
	assert.True(t, p.HasBoxscoreData)
}

func TestAddSkaterPlayer_SingleNameNoSplit(t *testing.T) {
	t.Parallel()

	players := make(map[int64]shared.PartialPlayer)
	skater := nhlapi.SkaterStats{
		PlayerID: nhlapi.PlayerID(1),
		Name:     nhlapi.LocalizedString{Default: "Gretzky"},
	}

	addSkaterPlayer(players, skater, 0)

	p := players[1]
	assert.Equal(t, "Gretzky", p.FirstName)
	assert.Equal(t, "", p.LastName)
}

func TestAddSkaterPlayer_OverwritesExisting(t *testing.T) {
	t.Parallel()

	players := make(map[int64]shared.PartialPlayer)
	players[42] = shared.PartialPlayer{ID: 42, FirstName: "Old", TeamID: 1}

	skater := nhlapi.SkaterStats{
		PlayerID: nhlapi.PlayerID(42),
		Name:     nhlapi.LocalizedString{Default: "New Name"},
		Position: "LW",
	}
	addSkaterPlayer(players, skater, 7)

	p := players[42]
	assert.Equal(t, "New", p.FirstName)
	assert.Equal(t, "Name", p.LastName)
	assert.Equal(t, int64(7), p.TeamID)
}

// --- addGoaliePlayer ---

func TestAddGoaliePlayer(t *testing.T) {
	t.Parallel()

	players := make(map[int64]shared.PartialPlayer)
	goalie := nhlapi.GoalieStats{
		PlayerID:      nhlapi.PlayerID(8479394),
		Name:          nhlapi.LocalizedString{Default: "Connor Hellebuyck"},
		Position:      "G",
		SweaterNumber: 37,
	}

	addGoaliePlayer(players, goalie, 52)

	require.Len(t, players, 1)
	p := players[8479394]
	assert.Equal(t, int64(8479394), p.ID)
	assert.Equal(t, "Connor", p.FirstName)
	assert.Equal(t, "Hellebuyck", p.LastName)
	assert.Equal(t, "G", p.Position)
	assert.Equal(t, 37, p.SweaterNumber)
	assert.Equal(t, int64(52), p.TeamID)
	assert.True(t, p.HasBoxscoreData)
}

func TestAddGoaliePlayer_SingleToken(t *testing.T) {
	t.Parallel()

	players := make(map[int64]shared.PartialPlayer)
	goalie := nhlapi.GoalieStats{
		PlayerID: nhlapi.PlayerID(9),
		Name:     nhlapi.LocalizedString{Default: "Fleury"},
	}

	addGoaliePlayer(players, goalie, 0)

	p := players[9]
	assert.Equal(t, "Fleury", p.FirstName)
	assert.Equal(t, "", p.LastName)
}

// --- playEventToParams ---

func TestPlayEventToParams_BasicFields(t *testing.T) {
	t.Parallel()

	gameID := nhlapi.GameID(2023020001)
	play := nhlapi.PlayEvent{
		EventID:       42,
		TypeCode:      505,
		TypeDescKey:   "shot-on-goal",
		SortOrder:     10,
		TimeInPeriod:  "12:34",
		TimeRemaining: "07:26",
		PeriodDescriptor: nhlapi.PeriodDescriptor{
			Number:     2,
			PeriodType: "REG",
		},
	}

	p := playEventToParams(gameID, play)

	assert.Equal(t, int64(gameID), p.GameID)
	assert.Equal(t, int64(42), p.EventID)
	assert.Equal(t, int32(2), p.Period)
	assert.Equal(t, sqlcdb.PeriodType("REG"), p.PeriodType)
	assert.Equal(t, "12:34", p.TimeInPeriod)
	assert.Equal(t, "07:26", p.TimeRemaining)
	assert.Equal(t, sqlcdb.PlayEventType("shot-on-goal"), p.TypeDescKey)
	assert.Equal(t, int32(10), p.SortOrder)
	assert.False(t, p.SituationCode.Valid)
	assert.False(t, p.HomeTeamDefendingSide.Valid)
}

func TestPlayEventToParams_OptionalTextFields(t *testing.T) {
	t.Parallel()

	play := nhlapi.PlayEvent{
		SituationCode:         "1551",
		HomeTeamDefendingSide: "left",
		PeriodDescriptor:      nhlapi.PeriodDescriptor{PeriodType: "REG"},
	}

	p := playEventToParams(0, play)

	assert.True(t, p.SituationCode.Valid)
	assert.Equal(t, int32(1551), p.SituationCode.Int32)
	assert.True(t, p.HomeTeamDefendingSide.Valid)
	assert.Equal(t, sqlcdb.IceSide("left"), p.HomeTeamDefendingSide.IceSide)
}

func TestPlayEventToParams_WithDetails(t *testing.T) {
	t.Parallel()

	xCoord := 45
	yCoord := -12
	shooterID := nhlapi.PlayerID(8478402)
	homeScore := 2
	awayScore := 1

	play := nhlapi.PlayEvent{
		PeriodDescriptor: nhlapi.PeriodDescriptor{PeriodType: "REG"},
		Details: &nhlapi.PlayEventDetails{
			XCoord:           &xCoord,
			YCoord:           &yCoord,
			ShootingPlayerID: &shooterID,
			HomeScore:        &homeScore,
			AwayScore:        &awayScore,
		},
	}

	p := playEventToParams(0, play)

	assert.True(t, p.XCoord.Valid)
	assert.Equal(t, int32(45), p.XCoord.Int32)
	assert.True(t, p.YCoord.Valid)
	assert.Equal(t, int32(-12), p.YCoord.Int32)
	assert.True(t, p.ShootingPlayerID.Valid)
	assert.Equal(t, int64(8478402), p.ShootingPlayerID.Int64)
	assert.True(t, p.HomeScore.Valid)
	assert.Equal(t, int32(2), p.HomeScore.Int32)
	assert.True(t, p.AwayScore.Valid)
	assert.Equal(t, int32(1), p.AwayScore.Int32)
}

func TestPlayEventToParams_NilDetails(t *testing.T) {
	t.Parallel()

	play := nhlapi.PlayEvent{
		PeriodDescriptor: nhlapi.PeriodDescriptor{PeriodType: "REG"},
		Details:          nil,
	}

	p := playEventToParams(0, play)

	assert.False(t, p.XCoord.Valid)
	assert.False(t, p.YCoord.Valid)
	assert.False(t, p.ShootingPlayerID.Valid)
}

// --- shiftEntryToParams ---

func TestShiftEntryToParams_RequiredFields(t *testing.T) {
	t.Parallel()

	shift := nhlapi.ShiftEntry{
		ID:          101,
		GameID:      nhlapi.GameID(2023020001),
		PlayerID:    nhlapi.PlayerID(8478402),
		TeamID:      nhlapi.TeamID(22),
		Period:      2,
		StartTime:   "05:00",
		EndTime:     "07:30",
		Duration:    "02:30",
		ShiftNumber: 14,
		TypeCode:    517,
		DetailCode:  0,
		EventNumber: 33,
	}

	p := shiftEntryToParams(shift)

	assert.Equal(t, int64(101), p.ID)
	assert.Equal(t, int64(2023020001), p.GameID)
	assert.Equal(t, int64(8478402), p.PlayerID)
	assert.Equal(t, int64(22), p.TeamID)
	assert.Equal(t, int32(2), p.Period)
	assert.Equal(t, "05:00", p.StartTime)
	assert.Equal(t, "07:30", p.EndTime)
	assert.Equal(t, "02:30", p.Duration)
	assert.Equal(t, int32(14), p.ShiftNumber)
	assert.Equal(t, sqlcdb.ShiftType("517"), p.TypeCode)
	assert.Equal(t, int64(33), p.EventNumber)
	assert.False(t, p.EventDescription.Valid)
}

func TestShiftEntryToParams_WithEventDescription(t *testing.T) {
	t.Parallel()

	desc := "GOAL"
	shift := nhlapi.ShiftEntry{
		EventDescription: &desc,
	}

	p := shiftEntryToParams(shift)

	assert.True(t, p.EventDescription.Valid)
	assert.Equal(t, "GOAL", p.EventDescription.String)
}

// --- collectPlayerIDs ---

func TestCollectPlayerIDs_Empty(t *testing.T) {
	t.Parallel()

	boxscore := &nhlapi.Boxscore{}
	seen := make(map[int64]struct{})

	collectPlayerIDs(boxscore, seen)

	assert.Empty(t, seen)
}

func TestCollectPlayerIDs_ForwardsAndDefense(t *testing.T) {
	t.Parallel()

	boxscore := &nhlapi.Boxscore{
		PlayerByGameStats: nhlapi.PlayerByGameStats{
			HomeTeam: nhlapi.TeamPlayerStats{
				Forwards: []nhlapi.SkaterStats{
					{PlayerID: nhlapi.PlayerID(1)},
					{PlayerID: nhlapi.PlayerID(2)},
				},
				Defense: []nhlapi.SkaterStats{
					{PlayerID: nhlapi.PlayerID(3)},
				},
			},
			AwayTeam: nhlapi.TeamPlayerStats{
				Forwards: []nhlapi.SkaterStats{
					{PlayerID: nhlapi.PlayerID(4)},
				},
				Defense: []nhlapi.SkaterStats{
					{PlayerID: nhlapi.PlayerID(5)},
				},
			},
		},
	}
	seen := make(map[int64]struct{})

	collectPlayerIDs(boxscore, seen)

	assert.Len(t, seen, 5)
	for _, id := range []int64{1, 2, 3, 4, 5} {
		assert.Contains(t, seen, id)
	}
}

func TestCollectPlayerIDs_Deduplication(t *testing.T) {
	t.Parallel()

	boxscore := &nhlapi.Boxscore{
		PlayerByGameStats: nhlapi.PlayerByGameStats{
			HomeTeam: nhlapi.TeamPlayerStats{
				Forwards: []nhlapi.SkaterStats{{PlayerID: nhlapi.PlayerID(99)}},
			},
			AwayTeam: nhlapi.TeamPlayerStats{
				Forwards: []nhlapi.SkaterStats{{PlayerID: nhlapi.PlayerID(99)}},
			},
		},
	}
	seen := make(map[int64]struct{})

	collectPlayerIDs(boxscore, seen)

	assert.Len(t, seen, 1)
}

func TestCollectPlayerIDs_GoaliesNotIncluded(t *testing.T) {
	t.Parallel()

	boxscore := &nhlapi.Boxscore{
		PlayerByGameStats: nhlapi.PlayerByGameStats{
			HomeTeam: nhlapi.TeamPlayerStats{
				Goalies: []nhlapi.GoalieStats{{PlayerID: nhlapi.PlayerID(999)}},
			},
		},
	}
	seen := make(map[int64]struct{})

	collectPlayerIDs(boxscore, seen)

	// Goalies are deliberately excluded by collectPlayerIDs.
	assert.Empty(t, seen)
}

// --- importPlayerGameLog ---

// mockPlayerGameLogUpdater is a minimal PlayerGameLogUpdater for testing.
type mockPlayerGameLogUpdater struct {
	calls  []sqlcdb.UpdateSkaterGameLogStatsParams
	retErr error
}

func (m *mockPlayerGameLogUpdater) UpdateSkaterGameLogStats(_ context.Context, arg sqlcdb.UpdateSkaterGameLogStatsParams) error {
	m.calls = append(m.calls, arg)
	return m.retErr
}

func TestImportPlayerGameLog_SkipsZeroStats(t *testing.T) {
	t.Parallel()

	q := &mockPlayerGameLogUpdater{}
	gameLog := &nhlapi.PlayerGameLog{
		GameLog: []nhlapi.GameLog{
			{GameID: nhlapi.GameID(1), PowerPlayPoints: 0, GameWinningGoals: nil, OTGoals: nil},
		},
	}

	updated, err := importPlayerGameLog(context.Background(), q, 100, gameLog)

	require.NoError(t, err)
	assert.Equal(t, 0, updated)
	assert.Empty(t, q.calls)
}

func TestImportPlayerGameLog_UpdatesNonZeroStats(t *testing.T) {
	t.Parallel()

	gwg := 1
	otg := 0
	q := &mockPlayerGameLogUpdater{}
	gameLog := &nhlapi.PlayerGameLog{
		GameLog: []nhlapi.GameLog{
			{GameID: nhlapi.GameID(2023020001), PowerPlayPoints: 2, GameWinningGoals: &gwg, OTGoals: &otg},
		},
	}

	updated, err := importPlayerGameLog(context.Background(), q, 8478402, gameLog)

	require.NoError(t, err)
	assert.Equal(t, 1, updated)
	require.Len(t, q.calls, 1)
	assert.Equal(t, int64(2023020001), q.calls[0].GameID)
	assert.Equal(t, int64(8478402), q.calls[0].PlayerID)
	assert.Equal(t, int16(2), q.calls[0].PowerPlayPoints)
	assert.Equal(t, int16(1), q.calls[0].GameWinningGoals)
	assert.Equal(t, int16(0), q.calls[0].OtGoals)
}

func TestImportPlayerGameLog_MultipleEntries(t *testing.T) {
	t.Parallel()

	gwg1 := 1
	q := &mockPlayerGameLogUpdater{}
	gameLog := &nhlapi.PlayerGameLog{
		GameLog: []nhlapi.GameLog{
			{GameID: nhlapi.GameID(1), PowerPlayPoints: 0},      // skip
			{GameID: nhlapi.GameID(2), PowerPlayPoints: 1},      // update
			{GameID: nhlapi.GameID(3), GameWinningGoals: &gwg1}, // update
		},
	}

	updated, err := importPlayerGameLog(context.Background(), q, 42, gameLog)

	require.NoError(t, err)
	assert.Equal(t, 2, updated)
	assert.Len(t, q.calls, 2)
}

func TestImportPlayerGameLog_EmptyLog(t *testing.T) {
	t.Parallel()

	q := &mockPlayerGameLogUpdater{}
	gameLog := &nhlapi.PlayerGameLog{GameLog: nil}

	updated, err := importPlayerGameLog(context.Background(), q, 1, gameLog)

	require.NoError(t, err)
	assert.Equal(t, 0, updated)
}

// --- importClubSkaterStats and importClubGoalieStats ---

// nilClubStatsUpserter is a ClubStatsUpserter whose batch methods should never
// be called; it panics if they are, catching accidental invocations.
type nilClubStatsUpserter struct{}

func (nilClubStatsUpserter) GetSeasonTeamAbbrevs(_ context.Context, _ int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error) {
	return nil, nil
}

func (nilClubStatsUpserter) UpsertClubSkaterStatsBatch(_ context.Context, _ []sqlcdb.UpsertClubSkaterStatsBatchParams) *sqlcdb.UpsertClubSkaterStatsBatchBatchResults {
	panic("UpsertClubSkaterStatsBatch called unexpectedly")
}

func (nilClubStatsUpserter) UpsertClubGoalieStatsBatch(_ context.Context, _ []sqlcdb.UpsertClubGoalieStatsBatchParams) *sqlcdb.UpsertClubGoalieStatsBatchBatchResults {
	panic("UpsertClubGoalieStatsBatch called unexpectedly")
}

// TestImportClubSkaterStats_Empty verifies that an empty skaters slice causes an
// early return without invoking any database batch method.
func TestImportClubSkaterStats_Empty(t *testing.T) {
	t.Parallel()

	stats := &nhlapi.ClubStats{Skaters: nil}
	team := sqlcdb.GetSeasonTeamAbbrevsRow{TeamID: 22, Abbrev: "EDM"}

	err := importClubSkaterStats(context.Background(), nilClubStatsUpserter{}, stats, team, 20232024, nhlapi.GameTypeRegularSeason)

	require.NoError(t, err)
}

// TestImportClubGoalieStats_Empty verifies that an empty goalies slice causes an
// early return without invoking any database batch method.
func TestImportClubGoalieStats_Empty(t *testing.T) {
	t.Parallel()

	stats := &nhlapi.ClubStats{Goalies: nil}
	team := sqlcdb.GetSeasonTeamAbbrevsRow{TeamID: 22, Abbrev: "EDM"}

	err := importClubGoalieStats(context.Background(), nilClubStatsUpserter{}, stats, team, 20232024, nhlapi.GameTypeRegularSeason)

	require.NoError(t, err)
}

// --- pgtype helpers (pf32, pi32) ---

// Both helpers always emit Valid=true. Production code uses them when the
// caller has already determined the value is meaningful, so a NULL/zero
// distinction would only be added if the contract changed — pin Valid=true
// here so that change has to be deliberate.

func TestPF32(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   float64
		want pgtype.Float4
	}{
		{"positive", 1.5, pgtype.Float4{Float32: 1.5, Valid: true}},
		{"zero", 0, pgtype.Float4{Float32: 0, Valid: true}},
		{"negative", -42.25, pgtype.Float4{Float32: -42.25, Valid: true}},
		{"narrowing truncates", 0.1, pgtype.Float4{Float32: float32(0.1), Valid: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, pf32(tc.in))
		})
	}
}

func TestPI32(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   int
		want pgtype.Int4
	}{
		{"positive", 100, pgtype.Int4{Int32: 100, Valid: true}},
		{"zero", 0, pgtype.Int4{Int32: 0, Valid: true}},
		{"negative", -7, pgtype.Int4{Int32: -7, Valid: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, pi32(tc.in))
		})
	}
}

// --- shouldInvalidate (FetchEdgeInput, FetchEdgeTeamInput) ---

// shouldInvalidate passes RefreshCurrent through unchanged: the workflow
// decides which seasons to refresh (explicit range or the latest season),
// and the activity must not re-gate it on the wall clock. The old
// IsCurrentSeason gate made the flag a silent no-op after nhl.Current()'s
// July-1 rollover for a season whose playoffs had just ended.
func TestFetchEdgeInput_ShouldInvalidate(t *testing.T) {
	t.Parallel()

	assert.False(t, FetchEdgeInput{Season: 2020, RefreshCurrent: false}.shouldInvalidate())
	assert.True(t, FetchEdgeInput{Season: 2020, RefreshCurrent: true}.shouldInvalidate())
}

func TestFetchEdgeTeamInput_ShouldInvalidate(t *testing.T) {
	t.Parallel()

	assert.False(t, FetchEdgeTeamInput{Season: 2020, RefreshCurrent: false}.shouldInvalidate())
	assert.True(t, FetchEdgeTeamInput{Season: 2020, RefreshCurrent: true}.shouldInvalidate())
}
