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
		DivisionName:     "Atlantic",
		DivisionAbbrev:   "A",
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
		HomeTeamName:      "Montréal Canadiens",
		HomeTeamAbbrev:    "MTL",
		AwayTeamID:        10,
		AwayTeamScore:     2,
		AwayTeamSog:       25,
		AwayTeamName:      "Toronto Maple Leafs",
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
		FirstName:     "Jake",
		LastName:      "Allen",
		TeamName:      "Montréal Canadiens",
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
