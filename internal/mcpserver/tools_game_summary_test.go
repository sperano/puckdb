package mcpserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	summaryGameID     int64 = 2025020001
	summaryHomeTeamID int64 = 8
	summaryAwayTeamID int64 = 10
	summarySuzukiID   int64 = 8480018
	summaryMatthewsID int64 = 8479318
	summaryDachID     int64 = 8481540
	summaryQuillanID  int64 = 8483678
)

// summaryQueryFake serves fixed rows and records which queries ran.
type summaryQueryFake struct {
	calls      []string
	game       sqlcdb.GetGameRow
	gameErr    error
	failQuery  string // name of the section query that fails with errSummaryQuery
	threeStars []sqlcdb.GetGameThreeStarsRow
	goals      []sqlcdb.GetGoalHighlightsRow
	shootout   []sqlcdb.GetShootoutAttemptsRow
	officials  []sqlcdb.GameOfficial
	coaches    []sqlcdb.GetGameCoachesRow
	scratches  []sqlcdb.GetGameScratchesRow
	goalEvents []sqlcdb.PlayEvent
	eventArgs  []sqlcdb.GetGamePlayEventsParams
}

var errSummaryQuery = errors.New("connection reset")

func (f *summaryQueryFake) record(name string) error {
	f.calls = append(f.calls, name)
	if name == f.failQuery {
		return errSummaryQuery
	}
	return nil
}

func (f *summaryQueryFake) GetGame(context.Context, int64) (sqlcdb.GetGameRow, error) {
	f.calls = append(f.calls, "GetGame")
	return f.game, f.gameErr
}

func (f *summaryQueryFake) GetGameThreeStars(context.Context, int64) ([]sqlcdb.GetGameThreeStarsRow, error) {
	return f.threeStars, f.record("GetGameThreeStars")
}

func (f *summaryQueryFake) GetGoalHighlights(context.Context, int64) ([]sqlcdb.GetGoalHighlightsRow, error) {
	return f.goals, f.record("GetGoalHighlights")
}

func (f *summaryQueryFake) GetShootoutAttempts(context.Context, int64) ([]sqlcdb.GetShootoutAttemptsRow, error) {
	return f.shootout, f.record("GetShootoutAttempts")
}

func (f *summaryQueryFake) GetGameOfficials(context.Context, int64) ([]sqlcdb.GameOfficial, error) {
	return f.officials, f.record("GetGameOfficials")
}

func (f *summaryQueryFake) GetGameCoaches(context.Context, int64) ([]sqlcdb.GetGameCoachesRow, error) {
	return f.coaches, f.record("GetGameCoaches")
}

func (f *summaryQueryFake) GetGameScratches(context.Context, int64) ([]sqlcdb.GetGameScratchesRow, error) {
	return f.scratches, f.record("GetGameScratches")
}

func (f *summaryQueryFake) GetGamePlayEvents(_ context.Context, arg sqlcdb.GetGamePlayEventsParams) ([]sqlcdb.PlayEvent, error) {
	f.eventArgs = append(f.eventArgs, arg)
	return f.goalEvents, f.record("GetGamePlayEvents")
}

func centerPosition() sqlcdb.NullPlayerPosition {
	return sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}
}

// newSummaryQueryFake is a shootout win of the away team with a row in
// every section; the coaches and scratches come home team first, as the
// queries may return them.
func newSummaryQueryFake() *summaryQueryFake {
	return &summaryQueryFake{
		game: sqlcdb.GetGameRow{
			ID: summaryGameID, Season: testSeasonID, GameType: sqlcdb.GameTypeRegularSeason,
			GameDate:  pgtype.Date{Time: time.Date(2025, 10, 8, 0, 0, 0, 0, time.UTC), Valid: true},
			GameState: sqlcdb.GameStateOFF, PeriodType: sqlcdb.PeriodTypeSO,
			HomeTeamID: summaryHomeTeamID, HomeTeamAbbrev: "MTL", HomeTeamScore: 2,
			AwayTeamID: summaryAwayTeamID, AwayTeamAbbrev: "TOR", AwayTeamScore: 3,
		},
		threeStars: []sqlcdb.GetGameThreeStarsRow{
			{GameID: summaryGameID, Star: 1, PlayerID: summarySuzukiID, FirstName: "Nick", LastName: "Suzuki", Position: centerPosition()},
		},
		goals: []sqlcdb.GetGoalHighlightsRow{
			{GameID: summaryGameID, EventID: 101, PlayerID: summarySuzukiID, Period: 1, TimeInPeriod: "05:12",
				GoalsToDate: pgtype.Int2{Int16: 1, Valid: true}, HighlightClipID: pgtype.Int8{Int64: 6363, Valid: true},
				HighlightClipUrl: pgtype.Text{String: "https://nhl.com/video/c-6363", Valid: true}, FirstName: "Nick", LastName: "Suzuki"},
			{GameID: summaryGameID, EventID: 202, PlayerID: summaryMatthewsID, Period: 2, TimeInPeriod: "10:00",
				FirstName: "Auston", LastName: "Matthews"},
		},
		// Only the first goal has a play-by-play event.
		goalEvents: []sqlcdb.PlayEvent{
			{GameID: summaryGameID, EventID: 101, TypeDescKey: sqlcdb.PlayEventTypeGoal,
				EventOwnerTeamID: pgtype.Int8{Int64: summaryHomeTeamID, Valid: true},
				AwayScore:        pgtype.Int4{Int32: 0, Valid: true}, HomeScore: pgtype.Int4{Int32: 1, Valid: true}},
		},
		shootout: []sqlcdb.GetShootoutAttemptsRow{
			{GameID: summaryGameID, Sequence: 1, PlayerID: summaryMatthewsID, TeamID: summaryAwayTeamID, ShotType: "wrist",
				Result: sqlcdb.ShootoutResultGoal, GameWinner: true, FirstName: "Auston", LastName: "Matthews"},
		},
		officials: []sqlcdb.GameOfficial{
			{GameID: summaryGameID, Role: sqlcdb.OfficialRoleReferee, Sequence: 1, Name: "Ref One"},
			{GameID: summaryGameID, Role: sqlcdb.OfficialRoleLinesman, Sequence: 1, Name: "Line One"},
		},
		coaches: []sqlcdb.GetGameCoachesRow{
			{GameID: summaryGameID, TeamID: summaryHomeTeamID, HeadCoach: "Martin St. Louis", TeamAbbrev: "MTL"},
			{GameID: summaryGameID, TeamID: summaryAwayTeamID, HeadCoach: "Craig Berube", TeamAbbrev: "TOR"},
		},
		scratches: []sqlcdb.GetGameScratchesRow{
			{GameID: summaryGameID, TeamID: summaryHomeTeamID, PlayerID: summaryDachID, FirstName: "Kirby", LastName: "Dach", Position: centerPosition()},
			{GameID: summaryGameID, TeamID: summaryAwayTeamID, PlayerID: summaryQuillanID, FirstName: "Jacob", LastName: "Quillan", Position: centerPosition()},
		},
	}
}

func callGameSummary(t *testing.T, q gameSummaryQueries, args map[string]any) (string, bool) {
	t.Helper()
	result := callTool(t, gameSummaryTool(q).Handler, args)
	return resultText(t, result), result.IsError
}

const wantFullGameSummary = `# game_id=2025020001 season=20252026 game_type=regular_season game_date=2025-10-08 game_state=OFF period_type=SO away=TOR away_team_id=10 away_score=3 home=MTL home_team_id=8 home_score=2
# three_stars
star,player_id,first_name,last_name,position
1,8480018,Nick,Suzuki,C
# goals
period,time_in_period,team_abbrev,player_id,first_name,last_name,goals_to_date,away_score,home_score,event_id,clip_url
1,05:12,MTL,8480018,Nick,Suzuki,1,0,1,101,https://nhl.com/video/c-6363
2,10:00,,8479318,Auston,Matthews,,,,202,
# shootout
sequence,team_abbrev,player_id,first_name,last_name,shot_type,result,game_winner
1,TOR,8479318,Auston,Matthews,wrist,goal,true
# officials
role,sequence,name
referee,1,Ref One
linesman,1,Line One
# coaches
team_abbrev,head_coach
TOR,Craig Berube
MTL,Martin St. Louis
# scratches
team_abbrev,player_id,first_name,last_name,position
TOR,8483678,Jacob,Quillan,C
MTL,8481540,Kirby,Dach,C
`

func TestGameSummaryRendersEverySection(t *testing.T) {
	q := newSummaryQueryFake()
	text, isError := callGameSummary(t, q, map[string]any{"game_id": float64(summaryGameID)})
	require.False(t, isError, text)
	assert.Equal(t, wantFullGameSummary, text)
	assert.Equal(t, []string{
		"GetGame", "GetGameThreeStars", "GetGoalHighlights", "GetGamePlayEvents", "GetShootoutAttempts",
		"GetGameOfficials", "GetGameCoaches", "GetGameScratches",
	}, q.calls)
	assert.Equal(t, []sqlcdb.GetGamePlayEventsParams{{GameID: summaryGameID, TypeDescKeys: []string{"goal"}}}, q.eventArgs)
}

func TestGameSummaryEmptySectionsReadNoResults(t *testing.T) {
	q := &summaryQueryFake{game: newSummaryQueryFake().game}
	text, isError := callGameSummary(t, q, map[string]any{"game_id": float64(summaryGameID)})
	require.False(t, isError, text)
	want := "# game_id=2025020001 season=20252026 game_type=regular_season game_date=2025-10-08 game_state=OFF " +
		"period_type=SO away=TOR away_team_id=10 away_score=3 home=MTL home_team_id=8 home_score=2\n"
	for _, title := range []string{threeStarsSection, goalsSection, shootoutSection, officialsSection, coachesSection, scratchesSection} {
		want += commentPrefix + title + "\n" + noResultsText + "\n"
	}
	assert.Equal(t, want, text)
}

func TestGameSummaryUnknownGameReadsNoSection(t *testing.T) {
	q := &summaryQueryFake{gameErr: pgx.ErrNoRows}
	text, isError := callGameSummary(t, q, map[string]any{"game_id": float64(summaryGameID)})
	assert.True(t, isError)
	assert.Equal(t, "game_id 2025020001 not found (unknown game, or one of its teams has no season_teams row for its season)", text)
	assert.Equal(t, []string{"GetGame"}, q.calls)
}

func TestGameSummaryReportsFailedQuery(t *testing.T) {
	q := newSummaryQueryFake()
	q.failQuery = "GetGameOfficials"
	text, isError := callGameSummary(t, q, map[string]any{"game_id": float64(summaryGameID)})
	assert.True(t, isError)
	assert.Equal(t, "load officials of game 2025020001: connection reset", text)

	q = newSummaryQueryFake()
	q.gameErr = errSummaryQuery
	text, isError = callGameSummary(t, q, map[string]any{"game_id": float64(summaryGameID)})
	assert.True(t, isError)
	assert.Equal(t, "load game 2025020001: connection reset", text)
}

func TestGameSummaryRejectsMalformedGameID(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"missing":    {},
		"fractional": {"game_id": 2025020001.5},
		"zero":       {"game_id": float64(0)},
	} {
		t.Run(name, func(t *testing.T) {
			q := newSummaryQueryFake()
			_, isError := callGameSummary(t, q, args)
			assert.True(t, isError)
			assert.Empty(t, q.calls)
		})
	}
}

func TestGameSummaryTeamAbbrevFallsBackToID(t *testing.T) {
	s := gameSummary{game: newSummaryQueryFake().game}
	assert.Equal(t, "MTL", s.teamAbbrev(summaryHomeTeamID))
	assert.Equal(t, "TOR", s.teamAbbrev(summaryAwayTeamID))
	assert.Equal(t, "99", s.teamAbbrev(99))
}

func TestGameSummaryOrdersTeamsAwayFirst(t *testing.T) {
	s := gameSummary{game: newSummaryQueryFake().game}
	assert.Negative(t, s.compareAwayFirst(summaryAwayTeamID, summaryHomeTeamID))
	assert.Positive(t, s.compareAwayFirst(summaryHomeTeamID, summaryAwayTeamID))
	assert.Negative(t, s.compareAwayFirst(summaryHomeTeamID, 99))
	assert.Zero(t, s.compareAwayFirst(summaryHomeTeamID, summaryHomeTeamID))
}

// Fixture for the PostgreSQL-backed get_game_summary test: a season and
// IDs no import uses.
const (
	summaryDBSeason  = 30053006
	summaryDBGameID  = 3005020001
	summaryDBHomeID  = 9751
	summaryDBAwayID  = 9752
	summaryDBHomeFwd = 97510001
	summaryDBAwayFwd = 97510002
	summaryDBScratch = 97510003
	summaryDBAwaySc  = 97510004
)

// TestGameSummaryFromDatabase runs the tool against the real queries, with
// rows inserted out of order so every section's order comes from SQL (or,
// for coaches, from the away-first sort). Officials sort by the
// official_role enum's declaration order: referees before linesmen.
func TestGameSummaryFromDatabase(t *testing.T) {
	pool := openMCPTestDB(t)
	deleteSummaryFixture(t, pool)
	t.Cleanup(func() { deleteSummaryFixture(t, pool) })
	seedSummaryFixture(t, pool)

	text, isError := callGameSummary(t, sqlcdb.New(pool), map[string]any{"game_id": float64(summaryDBGameID)})
	require.False(t, isError, text)
	assert.Equal(t, `# game_id=3005020001 season=30053006 game_type=regular_season game_date=3005-11-02 game_state=FINAL period_type=SO away=AWY away_team_id=9752 away_score=4 home=HOM home_team_id=9751 home_score=3
# three_stars
star,player_id,first_name,last_name,position
1,97510002,Fixture,Away,LW
2,97510001,Fixture,Home,C
# goals
period,time_in_period,team_abbrev,player_id,first_name,last_name,goals_to_date,away_score,home_score,event_id,clip_url
1,03:15,AWY,97510002,Fixture,Away,7,1,0,11,https://example.test/clip/11
1,14:40,HOM,97510001,Fixture,Home,,1,1,12,
# shootout
sequence,team_abbrev,player_id,first_name,last_name,shot_type,result,game_winner
1,HOM,97510001,Fixture,Home,snap,save,false
2,AWY,97510002,Fixture,Away,wrist,goal,true
# officials
role,sequence,name
referee,1,Ref Eree
referee,2,Sec Ond
linesman,1,Lin Esman
# coaches
team_abbrev,head_coach
AWY,Away Coach
HOM,Home Coach
# scratches
team_abbrev,player_id,first_name,last_name,position
AWY,97510004,Fixture,Benched,G
HOM,97510003,Fixture,Scratched,D
`, text)

	text, isError = callGameSummary(t, sqlcdb.New(pool), map[string]any{"game_id": float64(summaryDBGameID + 1)})
	assert.True(t, isError)
	assert.Contains(t, text, "not found")
}

func seedSummaryFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(context.Background(), sql, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO seasons (id, standings_start, standings_end) VALUES ($1, '3005-10-01', '3006-04-30')`, summaryDBSeason)
	exec(`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
		VALUES ($1, $2, 'Home Club', 'HOM', 'Test', 'T'), ($1, $3, 'Away Club', 'AWY', 'Test', 'T')`,
		summaryDBSeason, summaryDBHomeID, summaryDBAwayID)
	exec(`INSERT INTO players (id, first_name, last_name, position)
		VALUES ($1, 'Fixture', 'Home', 'C'), ($2, 'Fixture', 'Away', 'LW'), ($3, 'Fixture', 'Scratched', 'D'),
		($4, 'Fixture', 'Benched', 'G')`,
		summaryDBHomeFwd, summaryDBAwayFwd, summaryDBScratch, summaryDBAwaySc)
	exec(`INSERT INTO games (id, season, game_type, game_date, game_state, period_type,
		home_team_id, home_team_score, away_team_id, away_team_score)
		VALUES ($1, $2, 'regular_season', '3005-11-02', 'FINAL', 'SO', $3, 3, $4, 4)`,
		summaryDBGameID, summaryDBSeason, summaryDBHomeID, summaryDBAwayID)
	exec(`INSERT INTO game_three_stars (game_id, star, player_id) VALUES ($1, 2, $2), ($1, 1, $3)`,
		summaryDBGameID, summaryDBHomeFwd, summaryDBAwayFwd)
	exec(`INSERT INTO goal_highlights (game_id, event_id, player_id, period, time_in_period, goals_to_date, highlight_clip_url)
		VALUES ($1, 12, $2, 1, '14:40', NULL, NULL), ($1, 11, $3, 1, '03:15', 7, 'https://example.test/clip/11')`,
		summaryDBGameID, summaryDBHomeFwd, summaryDBAwayFwd)
	// A shot between the goals checks that only goal events are read.
	exec(`INSERT INTO play_events (game_id, event_id, period, period_type, time_in_period, time_remaining,
		type_desc_key, sort_order, event_owner_team_id, away_score, home_score)
		VALUES ($1, 12, 1, 'REG', '14:40', '05:20', 'goal', 30, $2, 1, 1),
		       ($1, 13, 1, 'REG', '10:00', '10:00', 'shot-on-goal', 20, $2, NULL, NULL),
		       ($1, 11, 1, 'REG', '03:15', '16:45', 'goal', 10, $3, 1, 0)`,
		summaryDBGameID, summaryDBHomeID, summaryDBAwayID)
	exec(`INSERT INTO shootout_attempts (game_id, sequence, player_id, team_id, shot_type, result, game_winner)
		VALUES ($1, 2, $2, $3, 'wrist', 'goal', true), ($1, 1, $4, $5, 'snap', 'save', false)`,
		summaryDBGameID, summaryDBAwayFwd, summaryDBAwayID, summaryDBHomeFwd, summaryDBHomeID)
	exec(`INSERT INTO game_officials (game_id, role, sequence, name)
		VALUES ($1, 'referee', 2, 'Sec Ond'), ($1, 'linesman', 1, 'Lin Esman'), ($1, 'referee', 1, 'Ref Eree')`,
		summaryDBGameID)
	exec(`INSERT INTO game_coaches (game_id, team_id, head_coach) VALUES ($1, $2, 'Home Coach'), ($1, $3, 'Away Coach')`,
		summaryDBGameID, summaryDBHomeID, summaryDBAwayID)
	exec(`INSERT INTO game_scratches (game_id, team_id, player_id) VALUES ($1, $2, $3), ($1, $4, $5)`,
		summaryDBGameID, summaryDBHomeID, summaryDBScratch, summaryDBAwayID, summaryDBAwaySc)
}

// deleteSummaryFixture removes the fixture rows; deleting the game
// cascades to its per-game rows except play_events.
func deleteSummaryFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM play_events WHERE game_id = $1`, []any{summaryDBGameID}},
		{`DELETE FROM games WHERE id = $1`, []any{summaryDBGameID}},
		{`DELETE FROM players WHERE id IN ($1, $2, $3, $4)`, []any{summaryDBHomeFwd, summaryDBAwayFwd, summaryDBScratch, summaryDBAwaySc}},
		{`DELETE FROM season_teams WHERE season = $1`, []any{summaryDBSeason}},
		{`DELETE FROM seasons WHERE id = $1`, []any{summaryDBSeason}},
	} {
		_, err := pool.Exec(context.Background(), stmt.sql, stmt.args...)
		require.NoError(t, err)
	}
}
