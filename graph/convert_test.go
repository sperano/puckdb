package graph

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
)

func TestTextPtr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := textPtr(pgtype.Text{String: "hello", Valid: true})
		if got == nil || *got != "hello" {
			t.Errorf("expected 'hello', got %v", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		got := textPtr(pgtype.Text{})
		if got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestInt4Ptr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := int4Ptr(pgtype.Int4{Int32: 42, Valid: true})
		if got == nil || *got != 42 {
			t.Errorf("expected 42, got %v", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		got := int4Ptr(pgtype.Int4{})
		if got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestInt8Ptr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := int8Ptr(pgtype.Int8{Int64: 8475786, Valid: true})
		if got == nil || *got != 8475786 {
			t.Errorf("expected 8475786, got %v", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		got := int8Ptr(pgtype.Int8{})
		if got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestFloat4Ptr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := float4Ptr(pgtype.Float4{Float32: 0.925, Valid: true})
		if got == nil {
			t.Fatal("expected non-nil")
		}
		if *got < 0.924 || *got > 0.926 {
			t.Errorf("expected ~0.925, got %v", *got)
		}
	})
	t.Run("null", func(t *testing.T) {
		got := float4Ptr(pgtype.Float4{})
		if got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestBoolPtr(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		got := boolPtr(pgtype.Bool{Bool: true, Valid: true})
		if got == nil || !*got {
			t.Errorf("expected true, got %v", got)
		}
	})
	t.Run("false", func(t *testing.T) {
		got := boolPtr(pgtype.Bool{Bool: false, Valid: true})
		if got == nil || *got {
			t.Errorf("expected false, got %v", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		got := boolPtr(pgtype.Bool{})
		if got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestDateString(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		d := pgtype.Date{Time: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), Valid: true}
		if got := dateString(d); got != "2024-03-15" {
			t.Errorf("expected '2024-03-15', got %q", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		if got := dateString(pgtype.Date{}); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}

func TestDateStringPtr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		d := pgtype.Date{Time: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), Valid: true}
		got := dateStringPtr(d)
		if got == nil || *got != "2024-03-15" {
			t.Errorf("expected '2024-03-15', got %v", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		if got := dateStringPtr(pgtype.Date{}); got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestTimestampPtr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ts := time.Date(2024, 3, 15, 19, 0, 0, 0, time.UTC)
		got := timestampPtr(pgtype.Timestamptz{Time: ts, Valid: true})
		if got == nil || !got.Equal(ts) {
			t.Errorf("expected %v, got %v", ts, got)
		}
	})
	t.Run("null", func(t *testing.T) {
		if got := timestampPtr(pgtype.Timestamptz{}); got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestNullPositionPtr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := nullPositionPtr(sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true})
		if got == nil || *got != "C" {
			t.Errorf("expected 'C', got %v", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		if got := nullPositionPtr(sqlcdb.NullPlayerPosition{}); got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestNullHandSidePtr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := nullHandSidePtr(sqlcdb.NullHandSide{HandSide: sqlcdb.HandSideL, Valid: true})
		if got == nil || *got != "L" {
			t.Errorf("expected 'L', got %v", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		if got := nullHandSidePtr(sqlcdb.NullHandSide{}); got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestNullGoalieDecisionPtr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := nullGoalieDecisionPtr(sqlcdb.NullGoalieDecision{GoalieDecision: sqlcdb.GoalieDecisionW, Valid: true})
		if got == nil || *got != "W" {
			t.Errorf("expected 'W', got %v", got)
		}
	})
	t.Run("null", func(t *testing.T) {
		if got := nullGoalieDecisionPtr(sqlcdb.NullGoalieDecision{}); got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})
}

func TestParseDate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		d := parseDate("2024-03-15")
		if !d.Valid || d.Time.Year() != 2024 || d.Time.Month() != 3 || d.Time.Day() != 15 {
			t.Errorf("expected valid 2024-03-15, got valid=%v time=%v", d.Valid, d.Time)
		}
	})
	t.Run("invalid format", func(t *testing.T) {
		d := parseDate("not-a-date")
		if d.Valid {
			t.Errorf("expected invalid, got valid date %v", d.Time)
		}
	})
	t.Run("empty", func(t *testing.T) {
		d := parseDate("")
		if d.Valid {
			t.Errorf("expected invalid for empty string")
		}
	})
}

func TestOptionalInt4(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		v := 42
		got := optionalInt4(&v)
		if !got.Valid || got.Int32 != 42 {
			t.Errorf("expected valid 42, got %+v", got)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got := optionalInt4(nil)
		if got.Valid {
			t.Errorf("expected invalid, got %+v", got)
		}
	})
}

func TestOptionalInt8(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		v := int64(8475786)
		got := optionalInt8(&v)
		if !got.Valid || got.Int64 != 8475786 {
			t.Errorf("expected valid 8475786, got %+v", got)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got := optionalInt8(nil)
		if got.Valid {
			t.Errorf("expected invalid, got %+v", got)
		}
	})
}

func TestOptionalText(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		v := "hello"
		got := optionalText(&v)
		if !got.Valid || got.String != "hello" {
			t.Errorf("expected valid 'hello', got %+v", got)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got := optionalText(nil)
		if got.Valid {
			t.Errorf("expected invalid, got %+v", got)
		}
	})
}

func TestOptionalBool(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		v := true
		got := optionalBool(&v)
		if !got.Valid || !got.Bool {
			t.Errorf("expected valid true, got %+v", got)
		}
	})
	t.Run("false", func(t *testing.T) {
		v := false
		got := optionalBool(&v)
		if !got.Valid || got.Bool {
			t.Errorf("expected valid false, got %+v", got)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got := optionalBool(nil)
		if got.Valid {
			t.Errorf("expected invalid, got %+v", got)
		}
	})
}

func TestOptionalDate(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		v := "2024-03-15"
		got := optionalDate(&v)
		if !got.Valid || got.Time.Year() != 2024 {
			t.Errorf("expected valid 2024, got %+v", got)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got := optionalDate(nil)
		if got.Valid {
			t.Errorf("expected invalid, got %+v", got)
		}
	})
}

func TestOptionalGameType(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		v := "regular_season"
		got := optionalGameType(&v)
		if !got.Valid || got.GameType != sqlcdb.GameTypeRegularSeason {
			t.Errorf("expected valid regular_season, got %+v", got)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got := optionalGameType(nil)
		if got.Valid {
			t.Errorf("expected invalid, got %+v", got)
		}
	})
}

func TestOptionalGameState(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		v := "FINAL"
		got := optionalGameState(&v)
		if !got.Valid || got.GameState != sqlcdb.GameStateFINAL {
			t.Errorf("expected valid FINAL, got %+v", got)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got := optionalGameState(nil)
		if got.Valid {
			t.Errorf("expected invalid, got %+v", got)
		}
	})
}

// --- Entity conversion tests ---

func TestConvertSeason(t *testing.T) {
	s := sqlcdb.Season{
		ID:             20232024,
		StandingsStart: pgtype.Date{Time: time.Date(2023, 10, 10, 0, 0, 0, 0, time.UTC), Valid: true},
		StandingsEnd:   pgtype.Date{Time: time.Date(2024, 4, 18, 0, 0, 0, 0, time.UTC), Valid: true},
	}
	got := convertSeason(s)
	if got.ID != 20232024 {
		t.Errorf("expected ID 20232024, got %d", got.ID)
	}
	if got.StandingsStart == nil || *got.StandingsStart != "2023-10-10" {
		t.Errorf("expected '2023-10-10', got %v", got.StandingsStart)
	}
	if got.StandingsEnd == nil || *got.StandingsEnd != "2024-04-18" {
		t.Errorf("expected '2024-04-18', got %v", got.StandingsEnd)
	}
}

func TestConvertSeasonNullDates(t *testing.T) {
	s := sqlcdb.Season{ID: 19171918}
	got := convertSeason(s)
	if got.StandingsStart != nil {
		t.Errorf("expected nil standingsStart for old season")
	}
	if got.StandingsEnd != nil {
		t.Errorf("expected nil standingsEnd for old season")
	}
}

func TestConvertPlayer(t *testing.T) {
	p := sqlcdb.Player{
		ID:             8478402,
		FirstName:      "Connor",
		LastName:       "McDavid",
		TeamID:         pgtype.Int8{Int64: 22, Valid: true},
		Position:       sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true},
		ShootsCatches:  sqlcdb.NullHandSide{HandSide: sqlcdb.HandSideL, Valid: true},
		HeightInches:   pgtype.Int4{Int32: 73, Valid: true},
		WeightPounds:   pgtype.Int4{Int32: 193, Valid: true},
		BirthDate:      pgtype.Date{Time: time.Date(1997, 1, 13, 0, 0, 0, 0, time.UTC), Valid: true},
		BirthCity:       pgtype.Text{String: "Richmond Hill", Valid: true},
		BirthCountry:   pgtype.Text{String: "CAN", Valid: true},
		SweaterNumber:  pgtype.Int4{Int32: 97, Valid: true},
		IsActive:       true,
		HeadshotURL:    "https://example.com/mcdavid.jpg",
		DraftYear:      pgtype.Int4{Int32: 2015, Valid: true},
		DraftRound:     pgtype.Int4{Int32: 1, Valid: true},
		DraftPickInRound: pgtype.Int4{Int32: 1, Valid: true},
		DraftOverallPick: pgtype.Int4{Int32: 1, Valid: true},
	}
	got := convertPlayer(p)
	if got.ID != 8478402 {
		t.Errorf("expected ID 8478402, got %d", got.ID)
	}
	if got.FirstName != "Connor" || got.LastName != "McDavid" {
		t.Errorf("expected 'Connor McDavid', got '%s %s'", got.FirstName, got.LastName)
	}
	if got.TeamID == nil || *got.TeamID != 22 {
		t.Errorf("expected teamId 22, got %v", got.TeamID)
	}
	if got.Position == nil || *got.Position != "C" {
		t.Errorf("expected position 'C', got %v", got.Position)
	}
	if got.SweaterNumber == nil || *got.SweaterNumber != 97 {
		t.Errorf("expected sweater 97, got %v", got.SweaterNumber)
	}
	if !got.IsActive {
		t.Error("expected isActive true")
	}
	if got.DraftYear == nil || *got.DraftYear != 2015 {
		t.Errorf("expected draft year 2015, got %v", got.DraftYear)
	}
}

func TestConvertPlayerNullFields(t *testing.T) {
	p := sqlcdb.Player{
		ID:        8471675,
		FirstName: "Sidney",
		LastName:  "Crosby",
		IsActive:  true,
	}
	got := convertPlayer(p)
	if got.TeamID != nil {
		t.Errorf("expected nil teamId, got %v", *got.TeamID)
	}
	if got.Position != nil {
		t.Errorf("expected nil position, got %v", *got.Position)
	}
	if got.HeightInches != nil {
		t.Errorf("expected nil heightInches, got %v", *got.HeightInches)
	}
	if got.DraftYear != nil {
		t.Errorf("expected nil draftYear, got %v", *got.DraftYear)
	}
}

func TestConvertSeasonTeam(t *testing.T) {
	st := sqlcdb.SeasonTeam{
		Season:           20232024,
		TeamID:           8,
		FranchiseID:      pgtype.Int8{Int64: 1, Valid: true},
		FullName:         "Montréal Canadiens",
		Abbrev:           "MTL",
		LogoUrl:          pgtype.Text{String: "https://example.com/mtl.svg", Valid: true},
		DivisionName:     pgtype.Text{String: "Atlantic", Valid: true},
		DivisionAbbrev:   pgtype.Text{String: "A", Valid: true},
		ConferenceName:   pgtype.Text{String: "Eastern", Valid: true},
		ConferenceAbbrev: pgtype.Text{String: "E", Valid: true},
	}
	got := convertSeasonTeam(st)
	if got.TeamID != 8 {
		t.Errorf("expected teamId 8, got %d", got.TeamID)
	}
	if got.FullName != "Montréal Canadiens" {
		t.Errorf("expected 'Montréal Canadiens', got %q", got.FullName)
	}
	if got.LogoURL == nil || *got.LogoURL != "https://example.com/mtl.svg" {
		t.Errorf("expected logo URL, got %v", got.LogoURL)
	}
	if got.ConferenceName == nil || *got.ConferenceName != "Eastern" {
		t.Errorf("expected 'Eastern', got %v", got.ConferenceName)
	}
}

func TestConvertGetGameRow(t *testing.T) {
	g := sqlcdb.GetGameRow{
		ID:                2023020001,
		Season:            20232024,
		GameType:          sqlcdb.GameTypeRegularSeason,
		GameDate:          pgtype.Date{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		Venue:             "Bell Centre",
		VenueLocation:     "Montréal, QC",
		StartTimeUTC:      pgtype.Timestamptz{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		GameState:         sqlcdb.GameStateFINAL,
		GameScheduleState: sqlcdb.GameScheduleStateOK,
		PeriodNumber:      3,
		PeriodType:        sqlcdb.PeriodTypeREG,
		HomeTeamID:        8,
		HomeTeamScore:     4,
		HomeTeamSog:       32,
		HomeTeamName:      "Canadiens",
		HomeTeamAbbrev:    "MTL",
		AwayTeamID:        10,
		AwayTeamScore:     2,
		AwayTeamSog:       25,
		AwayTeamName:      "Maple Leafs",
		AwayTeamAbbrev:    "TOR",
	}
	got := convertGetGameRow(g)
	if got.ID != 2023020001 {
		t.Errorf("expected ID 2023020001, got %d", got.ID)
	}
	if got.GameDate != "2024-01-15" {
		t.Errorf("expected '2024-01-15', got %q", got.GameDate)
	}
	if got.GameType != "regular_season" {
		t.Errorf("expected 'regular_season', got %q", got.GameType)
	}
	if got.HomeTeamAbbrev != "MTL" {
		t.Errorf("expected 'MTL', got %q", got.HomeTeamAbbrev)
	}
	if got.AwayTeamScore != 2 {
		t.Errorf("expected away score 2, got %d", got.AwayTeamScore)
	}
	if got.StartTimeUtc == nil {
		t.Error("expected non-nil startTimeUtc")
	}
}

func TestConvertStandingsSnapshot(t *testing.T) {
	s := sqlcdb.StandingsSnapshot{
		Season:           20232024,
		Date:             pgtype.Date{Time: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		TeamID:           8,
		TeamAbbrev:       "MTL",
		Wins:             30,
		Losses:           35,
		OtLosses:         5,
		Points:           65,
		DivisionAbbrev:   "A",
		DivisionName:     "Atlantic",
		ConferenceAbbrev: pgtype.Text{String: "E", Valid: true},
		ConferenceName:   pgtype.Text{String: "Eastern", Valid: true},
	}
	got := convertStandingsSnapshot(s)
	if got.TeamAbbrev != "MTL" {
		t.Errorf("expected 'MTL', got %q", got.TeamAbbrev)
	}
	if got.Points != 65 {
		t.Errorf("expected 65 points, got %d", got.Points)
	}
	if got.Date != "2024-03-15" {
		t.Errorf("expected '2024-03-15', got %q", got.Date)
	}
}

func TestConvertGameSkaterStats(t *testing.T) {
	s := sqlcdb.GetGameSkaterStatsByGameRow{
		GameID:        2023020001,
		PlayerID:      8478402,
		TeamID:        22,
		IsHome:        false,
		SweaterNumber: 97,
		Position:      sqlcdb.PlayerPositionC,
		FirstName:     "Connor",
		LastName:      "McDavid",
		TeamName:      "Edmonton Oilers",
		TeamAbbrev:    "EDM",
		Goals:         2,
		Assists:       1,
		Points:        3,
		PlusMinus:     2,
		ShotsOnGoal:   5,
		TOISeconds:    1260,
		Shifts:        22,
		FaceoffWinningPctg: pgtype.Float4{Float32: 0.55, Valid: true},
		Hits:          3,
		BlockedShots:  0,
		PenaltyMinutes: 2,
	}
	got := convertGameSkaterStats(s)
	if got.PlayerID != 8478402 {
		t.Errorf("expected playerId 8478402, got %d", got.PlayerID)
	}
	if got.Goals != 2 || got.Assists != 1 || got.Points != 3 {
		t.Errorf("expected 2G-1A-3P, got %dG-%dA-%dP", got.Goals, got.Assists, got.Points)
	}
	if got.FaceoffWinningPctg == nil {
		t.Fatal("expected non-nil faceoff pctg")
	}
	if *got.FaceoffWinningPctg < 0.54 || *got.FaceoffWinningPctg > 0.56 {
		t.Errorf("expected ~0.55, got %v", *got.FaceoffWinningPctg)
	}
}

func TestConvertGameGoalieStats(t *testing.T) {
	g := sqlcdb.GetGameGoalieStatsByGameRow{
		GameID:        2023020001,
		PlayerID:      8477424,
		TeamID:        8,
		IsHome:        true,
		SweaterNumber: 34,
		FirstName:     "Sam",
		LastName:      "Montembeault",
		TeamName:      "Montreal Canadiens",
		TeamAbbrev:    "MTL",
		Decision:      sqlcdb.NullGoalieDecision{GoalieDecision: sqlcdb.GoalieDecisionW, Valid: true},
		Starter:       pgtype.Bool{Bool: true, Valid: true},
		ShotsAgainst:  25,
		Saves:         23,
		SavePctg:      pgtype.Float4{Float32: 0.920, Valid: true},
		GoalsAgainst:  2,
		TOISeconds:    3600,
	}
	got := convertGameGoalieStats(g)
	if got.Decision == nil || *got.Decision != "W" {
		t.Errorf("expected decision 'W', got %v", got.Decision)
	}
	if got.Starter == nil || !*got.Starter {
		t.Errorf("expected starter true, got %v", got.Starter)
	}
	if got.SavePctg == nil {
		t.Fatal("expected non-nil savePctg")
	}
	if got.ShotsAgainst != 25 || got.Saves != 23 {
		t.Errorf("expected 25 SA / 23 SV, got %d / %d", got.ShotsAgainst, got.Saves)
	}
}

func TestConvertPlayerSeasonTotal(t *testing.T) {
	pst := sqlcdb.PlayerSeasonTotal{
		PlayerID:     8478402,
		Season:       20232024,
		GameType:     sqlcdb.GameTypeRegularSeason,
		LeagueAbbrev: "NHL",
		TeamName:     "Edmonton Oilers",
		TeamID:       pgtype.Int8{Int64: 22, Valid: true},
		GamesPlayed:  82,
		Goals:        pgtype.Int4{Int32: 44, Valid: true},
		Assists:      pgtype.Int4{Int32: 75, Valid: true},
		Points:       pgtype.Int4{Int32: 119, Valid: true},
		PlusMinus:    pgtype.Int4{Int32: 20, Valid: true},
		PIM:          pgtype.Int4{Int32: 36, Valid: true},
	}
	got := convertPlayerSeasonTotal(pst)
	if got.LeagueAbbrev != "NHL" {
		t.Errorf("expected 'NHL', got %q", got.LeagueAbbrev)
	}
	if got.GamesPlayed != 82 {
		t.Errorf("expected 82 GP, got %d", got.GamesPlayed)
	}
	if got.Goals == nil || *got.Goals != 44 {
		t.Errorf("expected 44 goals, got %v", got.Goals)
	}
	if got.Pim == nil || *got.Pim != 36 {
		t.Errorf("expected 36 PIM, got %v", got.Pim)
	}
}

func TestConvertPlayerSeasonTotalMinorLeague(t *testing.T) {
	pst := sqlcdb.PlayerSeasonTotal{
		PlayerID:     8478402,
		Season:       20142015,
		GameType:     sqlcdb.GameTypeRegularSeason,
		LeagueAbbrev: "OHL",
		TeamName:     "Erie Otters",
		GamesPlayed:  47,
		Goals:        pgtype.Int4{Int32: 44, Valid: true},
	}
	got := convertPlayerSeasonTotal(pst)
	if got.TeamID != nil {
		t.Errorf("expected nil teamId for minor league, got %v", *got.TeamID)
	}
	if got.LeagueAbbrev != "OHL" {
		t.Errorf("expected 'OHL', got %q", got.LeagueAbbrev)
	}
}

func TestPtr(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		p := ptr("hello")
		if p == nil {
			t.Fatal("expected non-nil pointer")
		}
		if *p != "hello" {
			t.Errorf("expected 'hello', got %q", *p)
		}
	})
	t.Run("int", func(t *testing.T) {
		p := ptr(42)
		if p == nil {
			t.Fatal("expected non-nil pointer")
		}
		if *p != 42 {
			t.Errorf("expected 42, got %d", *p)
		}
	})
	t.Run("zero value still produces a pointer", func(t *testing.T) {
		p := ptr(0)
		if p == nil {
			t.Fatal("expected non-nil pointer for zero int")
		}
		if *p != 0 {
			t.Errorf("expected 0, got %d", *p)
		}
	})
}

func TestResolveGameType(t *testing.T) {
	one := 1
	two := 2
	three := 3
	four := 4
	unknown := 99

	tests := []struct {
		name string
		in   *int
		want sqlcdb.GameType
	}{
		{"nil falls back to regular season", nil, sqlcdb.GameTypeRegularSeason},
		{"1 → preseason", &one, sqlcdb.GameTypePreseason},
		{"2 → regular season", &two, sqlcdb.GameTypeRegularSeason},
		{"3 → playoffs", &three, sqlcdb.GameTypePlayoffs},
		{"4 → all-star", &four, sqlcdb.GameTypeAllStar},
		{"unknown defaults to regular season", &unknown, sqlcdb.GameTypeRegularSeason},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveGameType(tc.in); got != tc.want {
				t.Errorf("resolveGameType(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestConvertGamesByDateRow(t *testing.T) {
	g := sqlcdb.GetGamesByDateRow{
		ID:                2023020001,
		Season:            20232024,
		GameType:          sqlcdb.GameTypeRegularSeason,
		GameDate:          pgtype.Date{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		Venue:             "Bell Centre",
		VenueLocation:     "Montréal, QC",
		StartTimeUTC:      pgtype.Timestamptz{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		GameState:         sqlcdb.GameStateFINAL,
		GameScheduleState: sqlcdb.GameScheduleStateOK,
		PeriodNumber:      3,
		PeriodType:        sqlcdb.PeriodTypeREG,
		HomeTeamID:        8,
		HomeTeamScore:     4,
		HomeTeamSog:       32,
		HomeTeamName:      "Canadiens",
		HomeTeamAbbrev:    "MTL",
		AwayTeamID:        10,
		AwayTeamScore:     2,
		AwayTeamSog:       25,
		AwayTeamName:      "Maple Leafs",
		AwayTeamAbbrev:    "TOR",
	}
	got := convertGamesByDateRow(g)
	if got.ID != 2023020001 {
		t.Errorf("expected ID 2023020001, got %d", got.ID)
	}
	if got.GameDate != "2024-01-15" {
		t.Errorf("expected '2024-01-15', got %q", got.GameDate)
	}
	if got.GameType != "regular_season" {
		t.Errorf("expected 'regular_season', got %q", got.GameType)
	}
	if got.HomeTeamAbbrev != "MTL" {
		t.Errorf("expected 'MTL', got %q", got.HomeTeamAbbrev)
	}
	if got.AwayTeamScore != 2 {
		t.Errorf("expected away score 2, got %d", got.AwayTeamScore)
	}
	if got.StartTimeUtc == nil {
		t.Error("expected non-nil startTimeUtc")
	}
}

func TestConvertListGamesRow(t *testing.T) {
	g := sqlcdb.ListGamesRow{
		ID:                2023020001,
		Season:            20232024,
		GameType:          sqlcdb.GameTypeRegularSeason,
		GameDate:          pgtype.Date{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		Venue:             "Bell Centre",
		VenueLocation:     "Montréal, QC",
		StartTimeUTC:      pgtype.Timestamptz{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		GameState:         sqlcdb.GameStateFINAL,
		GameScheduleState: sqlcdb.GameScheduleStateOK,
		PeriodNumber:      3,
		PeriodType:        sqlcdb.PeriodTypeREG,
		HomeTeamID:        8,
		HomeTeamScore:     4,
		HomeTeamSog:       32,
		HomeTeamName:      "Canadiens",
		HomeTeamAbbrev:    "MTL",
		AwayTeamID:        10,
		AwayTeamScore:     2,
		AwayTeamSog:       25,
		AwayTeamName:      "Maple Leafs",
		AwayTeamAbbrev:    "TOR",
	}
	got := convertListGamesRow(g)
	if got.ID != 2023020001 {
		t.Errorf("expected ID 2023020001, got %d", got.ID)
	}
	if got.GameDate != "2024-01-15" {
		t.Errorf("expected '2024-01-15', got %q", got.GameDate)
	}
	if got.AwayTeamAbbrev != "TOR" {
		t.Errorf("expected 'TOR', got %q", got.AwayTeamAbbrev)
	}
}

func TestConvertSkaterGameLogEntry(t *testing.T) {
	s := sqlcdb.GetSkaterStatsByPlayerAndSeasonRow{
		GameID:             2023020001,
		GameDate:           pgtype.Date{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		GameType:           sqlcdb.GameTypeRegularSeason,
		TeamAbbrev:         "MTL",
		Goals:              2,
		Assists:            1,
		Points:             3,
		PlusMinus:          1,
		ShotsOnGoal:        5,
		TOISeconds:         1234,
		Shifts:             22,
		FaceoffWinningPctg: pgtype.Float4{Float32: 0.55, Valid: true},
		Hits:               4,
		BlockedShots:       1,
		PenaltyMinutes:     2,
		PowerPlayGoals:     1,
	}
	got := convertSkaterGameLogEntry(s)
	if got.GameID != 2023020001 {
		t.Errorf("expected GameID 2023020001, got %d", got.GameID)
	}
	if got.GameDate != "2024-01-15" {
		t.Errorf("expected '2024-01-15', got %q", got.GameDate)
	}
	if got.GameType != "regular_season" {
		t.Errorf("expected 'regular_season', got %q", got.GameType)
	}
	if got.Points != 3 {
		t.Errorf("expected 3 points, got %d", got.Points)
	}
	if got.ToiSeconds != 1234 {
		t.Errorf("expected 1234 TOI seconds, got %d", got.ToiSeconds)
	}
	if got.FaceoffWinningPctg == nil {
		t.Fatal("expected non-nil FaceoffWinningPctg")
	}
	if *got.FaceoffWinningPctg < 0.54 || *got.FaceoffWinningPctg > 0.56 {
		t.Errorf("expected ~0.55, got %f", *got.FaceoffWinningPctg)
	}
}

func TestConvertGoalieGameLogEntry(t *testing.T) {
	g := sqlcdb.GetGoalieStatsByPlayerAndSeasonRow{
		GameID:       2023020001,
		GameDate:     pgtype.Date{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		GameType:     sqlcdb.GameTypeRegularSeason,
		TeamAbbrev:   "MTL",
		Decision:     sqlcdb.NullGoalieDecision{GoalieDecision: sqlcdb.GoalieDecisionW, Valid: true},
		Starter:      pgtype.Bool{Bool: true, Valid: true},
		ShotsAgainst: 32,
		Saves:        30,
		SavePctg:     pgtype.Float4{Float32: 0.9375, Valid: true},
		GoalsAgainst: 2,
		TOISeconds:   3600,
	}
	got := convertGoalieGameLogEntry(g)
	if got.GameID != 2023020001 {
		t.Errorf("expected GameID 2023020001, got %d", got.GameID)
	}
	if got.GameDate != "2024-01-15" {
		t.Errorf("expected '2024-01-15', got %q", got.GameDate)
	}
	if got.Decision == nil || *got.Decision != "W" {
		t.Errorf("expected decision 'W', got %v", got.Decision)
	}
	if got.Starter == nil || !*got.Starter {
		t.Error("expected starter true")
	}
	if got.Saves != 30 {
		t.Errorf("expected 30 saves, got %d", got.Saves)
	}
	if got.SavePctg == nil {
		t.Fatal("expected non-nil SavePctg")
	}
	if *got.SavePctg < 0.93 || *got.SavePctg > 0.94 {
		t.Errorf("expected ~0.9375, got %f", *got.SavePctg)
	}
}

func TestPtrStringIfNotEmpty(t *testing.T) {
	t.Run("empty string returns nil", func(t *testing.T) {
		if got := ptrStringIfNotEmpty(""); got != nil {
			t.Errorf("expected nil for empty input, got %q", *got)
		}
	})
	t.Run("non-empty returns pointer to value", func(t *testing.T) {
		got := ptrStringIfNotEmpty("hello")
		if got == nil {
			t.Fatal("expected non-nil pointer for non-empty input")
		}
		if *got != "hello" {
			t.Errorf("expected 'hello', got %q", *got)
		}
	})
	// Whitespace must NOT be treated as empty — pin the contract so a
	// future change to TrimSpace-then-check is a deliberate behavior change.
	t.Run("whitespace is non-empty", func(t *testing.T) {
		got := ptrStringIfNotEmpty("   ")
		if got == nil || *got != "   " {
			t.Errorf("expected pointer to whitespace, got %v", got)
		}
	})
}

func TestConvertEdgeGoalieStats(t *testing.T) {
	stat := sqlcdb.EdgeGoalieStat{
		PlayerID:                 8478402,
		Season:                   20232024,
		GameType:                 sqlcdb.GameTypeRegularSeason,
		GaaValue:                 pgtype.Float4{Float32: 2.45, Valid: true},
		GaaPercentile:            pgtype.Float4{Float32: 0.85, Valid: true},
		GaaLeagueAvg:             pgtype.Float4{Float32: 2.90, Valid: true},
		GamesAbove900Value:       pgtype.Float4{Float32: 12, Valid: true},
		GamesAbove900Percentile:  pgtype.Float4{Float32: 0.75, Valid: true},
		GamesAbove900LeagueAvg:   pgtype.Float4{Float32: 9.5, Valid: true},
		GoalDiffPer60Value:       pgtype.Float4{Float32: 0.45, Valid: true},
		GoalDiffPer60Percentile:  pgtype.Float4{Float32: 0.80, Valid: true},
		GoalDiffPer60LeagueAvg:   pgtype.Float4{Float32: 0.0, Valid: true},
		GoalSupportAvgValue:      pgtype.Float4{Float32: 3.1, Valid: true},
		GoalSupportAvgPercentile: pgtype.Float4{Float32: 0.6, Valid: true},
		GoalSupportAvgLeagueAvg:  pgtype.Float4{Float32: 2.95, Valid: true},
		PointPctgValue:           pgtype.Float4{Float32: 0.65, Valid: true},
		PointPctgPercentile:      pgtype.Float4{Float32: 0.70, Valid: true},
		PointPctgLeagueAvg:       pgtype.Float4{Float32: 0.55, Valid: true},
	}
	locSummary := []sqlcdb.EdgeGoalieShotLocationSummary{
		{
			LocationCode:           "high",
			GoalsAgainst:           pgtype.Int4{Int32: 30, Valid: true},
			GoalsAgainstPercentile: pgtype.Float4{Float32: 0.5, Valid: true},
			GoalsAgainstLeagueAvg:  pgtype.Float4{Float32: 32, Valid: true},
			Saves:                  pgtype.Int4{Int32: 270, Valid: true},
			SavesPercentile:        pgtype.Float4{Float32: 0.6, Valid: true},
			SavesLeagueAvg:         pgtype.Float4{Float32: 260, Valid: true},
			SavePctg:               pgtype.Float4{Float32: 0.900, Valid: true},
			SavePctgPercentile:     pgtype.Float4{Float32: 0.65, Valid: true},
			SavePctgLeagueAvg:      pgtype.Float4{Float32: 0.890, Valid: true},
		},
	}
	locs := []sqlcdb.EdgeGoalieShotLocation{
		{
			Area:               "slot",
			Saves:              pgtype.Int4{Int32: 150, Valid: true},
			SavesPercentile:    pgtype.Float4{Float32: 0.7, Valid: true},
			SavePctg:           pgtype.Float4{Float32: 0.880, Valid: true},
			SavePctgPercentile: pgtype.Float4{Float32: 0.55, Valid: true},
		},
		{
			Area:               "perimeter",
			Saves:              pgtype.Int4{Int32: 220, Valid: true},
			SavesPercentile:    pgtype.Float4{Float32: 0.8, Valid: true},
			SavePctg:           pgtype.Float4{Float32: 0.945, Valid: true},
			SavePctgPercentile: pgtype.Float4{Float32: 0.75, Valid: true},
		},
	}

	got := convertEdgeGoalieStats(stat, locSummary, locs)

	if got.PlayerID != 8478402 {
		t.Errorf("expected PlayerID 8478402, got %d", got.PlayerID)
	}
	if got.Season != 20232024 {
		t.Errorf("expected Season 20232024, got %d", got.Season)
	}
	if got.GameType != "regular_season" {
		t.Errorf("expected 'regular_season', got %q", got.GameType)
	}
	if got.GaaValue == nil || *got.GaaValue < 2.44 || *got.GaaValue > 2.46 {
		t.Errorf("expected GaaValue ~2.45, got %v", got.GaaValue)
	}
	if len(got.ShotLocationSummary) != 1 {
		t.Fatalf("expected 1 ShotLocationSummary entry, got %d", len(got.ShotLocationSummary))
	}
	if got.ShotLocationSummary[0].LocationCode != "high" {
		t.Errorf("expected LocationCode 'high', got %q", got.ShotLocationSummary[0].LocationCode)
	}
	if len(got.ShotLocations) != 2 {
		t.Fatalf("expected 2 ShotLocations entries, got %d", len(got.ShotLocations))
	}
	if got.ShotLocations[0].Area != "slot" || got.ShotLocations[1].Area != "perimeter" {
		t.Errorf("ShotLocations order/areas unexpected: %q, %q",
			got.ShotLocations[0].Area, got.ShotLocations[1].Area)
	}
}

// TestConvertEdgeGoalieStats_EmptySlices pins the empty-slice behavior:
// the converter should produce empty (not nil) slices because the model
// fields use len(input) for sizing.
func TestConvertEdgeGoalieStats_EmptySlices(t *testing.T) {
	got := convertEdgeGoalieStats(
		sqlcdb.EdgeGoalieStat{PlayerID: 1, Season: 20232024, GameType: sqlcdb.GameTypeRegularSeason},
		nil,
		nil,
	)
	if got.ShotLocationSummary == nil {
		t.Error("expected non-nil (empty) ShotLocationSummary slice")
	}
	if len(got.ShotLocationSummary) != 0 {
		t.Errorf("expected empty ShotLocationSummary, got %d entries", len(got.ShotLocationSummary))
	}
	if got.ShotLocations == nil {
		t.Error("expected non-nil (empty) ShotLocations slice")
	}
	if len(got.ShotLocations) != 0 {
		t.Errorf("expected empty ShotLocations, got %d entries", len(got.ShotLocations))
	}
}

func TestConvertEdgeSkaterStats(t *testing.T) {
	stat := sqlcdb.EdgeSkaterStat{
		PlayerID:                  8478402,
		Season:                    20232024,
		GameType:                  sqlcdb.GameTypeRegularSeason,
		TopSpeedImperial:          pgtype.Float4{Float32: 23.5, Valid: true},
		TopSpeedMetric:            pgtype.Float4{Float32: 37.8, Valid: true},
		TopSpeedPercentile:        pgtype.Float4{Float32: 0.95, Valid: true},
		BurstsOver20:              pgtype.Int4{Int32: 145, Valid: true},
		BurstsOver20Percentile:    pgtype.Float4{Float32: 0.88, Valid: true},
		TotalDistanceImperial:     pgtype.Float4{Float32: 198.4, Valid: true},
		TopShotSpeedImperial:      pgtype.Float4{Float32: 99.5, Valid: true},
		MaxGameDistanceImperial:   pgtype.Float4{Float32: 5.2, Valid: true},
		MaxGameDistanceMetric:     pgtype.Float4{Float32: 8.4, Valid: true},
		MaxGameDistancePercentile: pgtype.Float4{Float32: 0.7, Valid: true},
		OzPctg:                    pgtype.Float4{Float32: 0.55, Valid: true},
		OzPercentile:              pgtype.Float4{Float32: 0.80, Valid: true},
	}
	locs := []sqlcdb.EdgeSkaterShotLocation{
		{
			Area:                   "slot",
			SOG:                    pgtype.Int4{Int32: 80, Valid: true},
			Goals:                  pgtype.Int4{Int32: 25, Valid: true},
			ShootingPctg:           pgtype.Float4{Float32: 0.3125, Valid: true},
			SogPercentile:          pgtype.Float4{Float32: 0.95, Valid: true},
			GoalsPercentile:        pgtype.Float4{Float32: 0.99, Valid: true},
			ShootingPctgPercentile: pgtype.Float4{Float32: 0.97, Valid: true},
		},
	}
	sog := []sqlcdb.EdgeSkaterSogSummary{
		{
			LocationCode:           "high",
			Shots:                  pgtype.Int4{Int32: 100, Valid: true},
			ShotsPercentile:        pgtype.Float4{Float32: 0.7, Valid: true},
			Goals:                  pgtype.Int4{Int32: 12, Valid: true},
			GoalsPercentile:        pgtype.Float4{Float32: 0.8, Valid: true},
			ShootingPctg:           pgtype.Float4{Float32: 0.12, Valid: true},
			ShootingPctgPercentile: pgtype.Float4{Float32: 0.65, Valid: true},
		},
	}

	got := convertEdgeSkaterStats(stat, locs, sog)

	if got.PlayerID != 8478402 {
		t.Errorf("expected PlayerID 8478402, got %d", got.PlayerID)
	}
	if got.GameType != "regular_season" {
		t.Errorf("expected 'regular_season', got %q", got.GameType)
	}
	if got.TopSpeedImperial == nil || *got.TopSpeedImperial < 23.4 || *got.TopSpeedImperial > 23.6 {
		t.Errorf("expected TopSpeedImperial ~23.5, got %v", got.TopSpeedImperial)
	}
	if got.BurstsOver20 == nil || *got.BurstsOver20 != 145 {
		t.Errorf("expected BurstsOver20 145, got %v", got.BurstsOver20)
	}
	if len(got.ShotLocations) != 1 || got.ShotLocations[0].Area != "slot" {
		t.Errorf("ShotLocations not mapped correctly: %+v", got.ShotLocations)
	}
	if len(got.SogSummary) != 1 || got.SogSummary[0].LocationCode != "high" {
		t.Errorf("SogSummary not mapped correctly: %+v", got.SogSummary)
	}
}

func TestConvertEdgeTeamStats(t *testing.T) {
	stat := sqlcdb.EdgeTeamStat{
		TeamID:                 10,
		Season:                 20232024,
		GameType:               sqlcdb.GameTypeRegularSeason,
		ShotAttemptsOver90:     pgtype.Int4{Int32: 320, Valid: true},
		ShotAttemptsOver90Rank: pgtype.Int4{Int32: 5, Valid: true},
		TopShotSpeedImperial:   pgtype.Float4{Float32: 102.3, Valid: true},
		TopShotSpeedRank:       pgtype.Int4{Int32: 2, Valid: true},
		BurstsOver22:           pgtype.Int4{Int32: 412, Valid: true},
		TotalDistance:          pgtype.Int4{Int32: 5500, Valid: true},
		OzPctg:                 pgtype.Float4{Float32: 0.52, Valid: true},
		OzRank:                 pgtype.Int4{Int32: 8, Valid: true},
	}
	sog := []sqlcdb.EdgeTeamSogSummary{
		{
			LocationCode:     "high",
			Shots:            pgtype.Int4{Int32: 850, Valid: true},
			ShotsRank:        pgtype.Int4{Int32: 3, Valid: true},
			Goals:            pgtype.Int4{Int32: 95, Valid: true},
			GoalsRank:        pgtype.Int4{Int32: 4, Valid: true},
			ShootingPctg:     pgtype.Float4{Float32: 0.112, Valid: true},
			ShootingPctgRank: pgtype.Int4{Int32: 6, Valid: true},
		},
	}
	locs := []sqlcdb.EdgeTeamShotLocation{
		{Area: "slot", Shots: pgtype.Int4{Int32: 280, Valid: true}, ShotsRank: pgtype.Int4{Int32: 2, Valid: true}},
		{Area: "perimeter", Shots: pgtype.Int4{Int32: 540, Valid: true}, ShotsRank: pgtype.Int4{Int32: 7, Valid: true}},
	}
	zt := []sqlcdb.EdgeTeamZoneTimeByStrength{
		{
			StrengthCode: "ev",
			OzPctg:       pgtype.Float4{Float32: 0.51, Valid: true},
			OzRank:       pgtype.Int4{Int32: 9, Valid: true},
			NzPctg:       pgtype.Float4{Float32: 0.20, Valid: true},
			NzRank:       pgtype.Int4{Int32: 14, Valid: true},
			DzPctg:       pgtype.Float4{Float32: 0.29, Valid: true},
			DzRank:       pgtype.Int4{Int32: 11, Valid: true},
		},
	}
	sd := &sqlcdb.EdgeTeamShotDifferential{
		ShotAttemptDifferential:     pgtype.Float4{Float32: 4.2, Valid: true},
		ShotAttemptDifferentialRank: pgtype.Int4{Int32: 6, Valid: true},
		SogDifferential:             pgtype.Float4{Float32: 1.1, Valid: true},
		SogDifferentialRank:         pgtype.Int4{Int32: 12, Valid: true},
	}

	got := convertEdgeTeamStats(stat, sog, locs, zt, sd)

	if got.TeamID != 10 {
		t.Errorf("expected TeamID 10, got %d", got.TeamID)
	}
	if got.GameType != "regular_season" {
		t.Errorf("expected 'regular_season', got %q", got.GameType)
	}
	if got.ShotAttemptsOver90 == nil || *got.ShotAttemptsOver90 != 320 {
		t.Errorf("expected ShotAttemptsOver90 320, got %v", got.ShotAttemptsOver90)
	}
	if len(got.SogSummary) != 1 || got.SogSummary[0].LocationCode != "high" {
		t.Errorf("SogSummary not mapped correctly: %+v", got.SogSummary)
	}
	if len(got.ShotLocations) != 2 {
		t.Fatalf("expected 2 ShotLocations, got %d", len(got.ShotLocations))
	}
	if got.ShotLocations[1].Area != "perimeter" {
		t.Errorf("ShotLocations order wrong: %q", got.ShotLocations[1].Area)
	}
	if len(got.ZoneTimeByStrength) != 1 || got.ZoneTimeByStrength[0].StrengthCode != "ev" {
		t.Errorf("ZoneTimeByStrength not mapped correctly: %+v", got.ZoneTimeByStrength)
	}
	if got.ShotDifferential == nil {
		t.Fatal("expected non-nil ShotDifferential when sd is non-nil")
	}
	if got.ShotDifferential.ShotAttemptDifferential == nil ||
		*got.ShotDifferential.ShotAttemptDifferential < 4.1 ||
		*got.ShotDifferential.ShotAttemptDifferential > 4.3 {
		t.Errorf("ShotAttemptDifferential not mapped: %+v", got.ShotDifferential)
	}
}

// TestConvertEdgeTeamStats_NilShotDifferential pins the documented branch:
// when sd is nil, the resulting model.ShotDifferential must also be nil
// (not a zeroed struct). GraphQL clients distinguish "no data" via nil.
func TestConvertEdgeTeamStats_NilShotDifferential(t *testing.T) {
	got := convertEdgeTeamStats(
		sqlcdb.EdgeTeamStat{TeamID: 10, Season: 20232024, GameType: sqlcdb.GameTypeRegularSeason},
		nil, nil, nil, nil,
	)
	if got.ShotDifferential != nil {
		t.Errorf("expected nil ShotDifferential when sd is nil, got %+v", got.ShotDifferential)
	}
}
