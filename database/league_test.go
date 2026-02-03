package database

import (
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestScanPositionType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
		want PositionType
		err  error
	}{
		{
			name: "skater",
			data: "P",
			want: Skater,
			err:  nil,
		},
		{
			name: "goaltender",
			data: "G",
			want: Goaltender,
			err:  nil,
		},
		{
			name: "both",
			data: "",
			want: BothPositions,
			err:  nil,
		},
		{
			name: "invalid",
			data: "invalid",
			want: 0,
			err:  fmt.Errorf("invalid position type: %s", "invalid"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt, err := ScanPositionType(tt.data)
			assert.Equal(t, tt.want, pt)
			assert.Equal(t, tt.err, err)
		})
	}
}

func TestScanStatGroup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
		want StatGroup
		err  error
	}{
		{
			name: "offense",
			data: "offense",
			want: Offense,
			err:  nil,
		},
		{
			name: "goaltending",
			data: "goaltending",
			want: Goaltending,
			err:  nil,
		},
		{
			name: "invalid",
			data: "invalid",
			want: 0,
			err:  fmt.Errorf("invalid stat group: invalid"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sg, err := ScanStatGroup(tt.data)
			assert.Equal(t, tt.err, err)
			assert.Equal(t, tt.want, sg)
		})
	}
}
func TestLeagueEnsure(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt := time.Date(2022, 9, 30, 10, 15, 25, 0, time.UTC)
	startDate := time.Date(2022, 5, 22, 10, 15, 25, 0, time.UTC)
	endDate := time.Date(2022, 4, 30, 10, 15, 25, 0, time.UTC)
	fileTS := time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC)
	tradeEndDate := time.Date(2022, 5, 24, 10, 15, 27, 0, time.UTC)
	l := &League{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		Key:                   "my-key",
		Name:                  "my-name",
		URL:                   "my-url",
		LogoURL:               "my-logo-url",
		DraftStatus:           "my-draft-status",
		NumTeams:              11,
		EditKey:               "my-edit-key",
		LeagueUpdateTimestamp: 123,
		ScoringType:           "my-scoring-type",
		LeagueType:            "my-league-type",
		IsProLeague:           true,
		IsCashLeague:          true,
		StartDate:             startDate,
		EndDate:               endDate,
		GameCode:              "my-game-code",
		Season:                2022,
		DraftType:             "my-draft-type",
		IsAuctionDraft:        true,
		PersistentURL:         "my-persistent-url",
		UsesPlayoff:           true,
		WaiverType:            "my-waiver-type",
		WaiverRule:            "my-waiver-rule",
		DraftTime:             123,
		DraftPickTime:         456,
		PostDraftPlayers:      "my-post-draft-players",
		MaxTeams:              12,
		WaiverTime:            78,
		TradeEndDate:          tradeEndDate,
		TradeRatifyType:       "my-trade-ratify-type",
		TradeRejectTime:       90,
		PlayerPool:            "my-player-pool",
		CantCutList:           "my-cant-cut-list",
		SendbirdChannelURL:    "my-sendbird-channel-url",
		SourceVersion:         fileTS,
	}
	gormdb, db, mock := sqlmockNew(t)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	stmt := fmt.Sprintf(`INSERT INTO "%s" `+
		`("%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s",`+
		`"%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s") `+
		`VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,`+
		`$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38) ON CONFLICT ("%s") DO UPDATE SET %s RETURNING "%s"`,
		TblLeagues, CCreatedAt, CUpdatedAt, CDeletedAt, CKey, CName, CURL, CLogoURL, CDraftStatus, CNumTeams, CEditKey,
		CLeagueUpdateTimestamp, CScoringType, CLeagueType, CIsProLeague, CIsCashLeague, CStartDate, CEndDate,
		CGameCode, CSeason, CDraftType, CIsAuctionDraft, CPersistentURL, CUsesPlayoff, CWaiverType, CWaiverRule, CDraftTime, CDrafTPickTime,
		CPostDraftPlayers, CMaxTeams, CWaiverTime, CTradeEndDate, CTradeRatifyType, CTradeRejectTime, CPlayerPool, CCantCutList, CSendbirdChannelURL,
		CSourceVersion, CID, CID, upexes(leagueUpdateCols), CID)
	mock.ExpectQuery(regexp.QuoteMeta(stmt)).
		WithArgs(createdAt, updatedAt, nil, "my-key", "my-name", "my-url", "my-logo-url", "my-draft-status", 11, "my-edit-key", 123,
			"my-scoring-type", "my-league-type", true, true, startDate, endDate, "my-game-code", 2022, "my-draft-type",
			true, "my-persistent-url", true, "my-waiver-type", "my-waiver-rule", 123, 456, "my-post-draft-players", 12, 78,
			tradeEndDate, "my-trade-ratify-type", 90, "my-player-pool", "my-cant-cut-list", "my-sendbird-channel-url",
			fileTS, 76).
		WillReturnRows(sqlmock.NewRows([]string{CID}).AddRow(76))
	mock.ExpectCommit()
	if err := l.Ensure(gormdb); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err.Error())
	}
}

func TestLeague_SetSourceVersion(t *testing.T) {
	t.Parallel()
	l := &League{}
	assert.Equal(t, time.Time{}, l.SourceVersion)
	l.SetSourceVersion(time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC))
	assert.Equal(t, time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC), l.SourceVersion)
}

func TestLeagueGraphQLModel(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt := time.Date(2022, 9, 30, 10, 15, 25, 0, time.UTC)
	startDate := time.Date(2022, 5, 22, 10, 15, 25, 0, time.UTC)
	endDate := time.Date(2022, 4, 30, 10, 15, 25, 0, time.UTC)
	fileTS := time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC)
	l := &League{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		Key:                   "my-key",
		Name:                  "my-name",
		URL:                   "my-url",
		LogoURL:               "my-logo-url",
		DraftStatus:           "my-draft-status",
		NumTeams:              11,
		EditKey:               "my-edit-key",
		LeagueUpdateTimestamp: 123,
		ScoringType:           "my-scoring-type",
		LeagueType:            "my-league-type",
		IsProLeague:           true,
		IsCashLeague:          true,
		StartDate:             startDate,
		EndDate:               endDate,
		GameCode:              "my-game-code",
		Season:                2022,
		SourceVersion:         fileTS,
	}
	g := l.GraphQLModel()
	assert.Equal(t, 76, g.ID)
	assert.Equal(t, "my-key", g.Key)
	assert.Equal(t, "my-name", g.Name)
	assert.Equal(t, "my-url", g.URL)
	assert.Equal(t, "my-logo-url", g.LogoURL)
	assert.Equal(t, "my-draft-status", g.DraftStatus)
	assert.Equal(t, 11, g.NumTeams)
	assert.Equal(t, "my-edit-key", g.EditKey)
	assert.Equal(t, int64(123), g.LeagueUpdateTimestamp)
	assert.Equal(t, "my-scoring-type", g.ScoringType)
	assert.Equal(t, "my-league-type", g.LeagueType)
	assert.True(t, g.IsProLeague)
	assert.True(t, g.IsCashLeague)
	assert.Equal(t, startDate, g.StartDate)
	assert.Equal(t, endDate, g.EndDate)
	assert.Equal(t, "my-game-code", g.GameCode)
	assert.Equal(t, createdAt, g.CreatedAt)
	assert.Equal(t, updatedAt, g.UpdatedAt)
}

func TestLeagueDiffSame(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt := time.Date(2022, 9, 30, 10, 15, 25, 0, time.UTC)
	startDate := time.Date(2022, 5, 22, 10, 15, 25, 0, time.UTC)
	endDate := time.Date(2022, 4, 30, 10, 15, 25, 0, time.UTC)
	l := &League{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		Key:                   "my-key",
		Name:                  "my-name",
		URL:                   "my-url",
		LogoURL:               "my-logo-url",
		DraftStatus:           "my-draft-status",
		NumTeams:              11,
		EditKey:               "my-edit-key",
		LeagueUpdateTimestamp: 123,
		ScoringType:           "my-scoring-type",
		LeagueType:            "my-league-type",
		IsProLeague:           true,
		IsCashLeague:          true,
		StartDate:             startDate,
		EndDate:               endDate,
		GameCode:              "my-game-code",
		Season:                2022,
	}
	diffs := DiffLeague(l, l)
	assert.Empty(t, diffs)
}

func TestLeagueDiff(t *testing.T) {
	t.Parallel()
	createdAt1 := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt1 := time.Date(2022, 9, 30, 10, 15, 25, 0, time.UTC)
	startDate1 := time.Date(2022, 5, 22, 10, 15, 25, 0, time.UTC)
	endDate1 := time.Date(2022, 4, 30, 10, 15, 25, 0, time.UTC)
	l1 := &League{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt1,
			UpdatedAt: updatedAt1,
		},
		Key:                   "my-key",
		Name:                  "my-name",
		URL:                   "my-url",
		LogoURL:               "my-logo-url",
		DraftStatus:           "my-draft-status",
		NumTeams:              11,
		EditKey:               "my-edit-key",
		LeagueUpdateTimestamp: 123,
		ScoringType:           "my-scoring-type",
		LeagueType:            "my-league-type",
		IsProLeague:           true,
		IsCashLeague:          true,
		StartDate:             startDate1,
		EndDate:               endDate1,
		GameCode:              "my-game-code",
		Season:                2022,
	}
	createdAt2 := time.Date(2023, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt2 := time.Date(2023, 9, 30, 10, 15, 25, 0, time.UTC)
	startDate2 := time.Date(2023, 5, 22, 10, 15, 25, 0, time.UTC)
	endDate2 := time.Date(2023, 4, 30, 10, 15, 25, 0, time.UTC)
	l2 := &League{
		Model: gorm.Model{
			ID:        83,
			CreatedAt: createdAt2,
			UpdatedAt: updatedAt2,
		},
		Key:                   "my-key2",
		Name:                  "my-name2",
		URL:                   "my-url2",
		LogoURL:               "my-logo-url2",
		DraftStatus:           "my-draft-status2",
		NumTeams:              12,
		EditKey:               "my-edit-key2",
		LeagueUpdateTimestamp: 1232,
		ScoringType:           "my-scoring-type2",
		LeagueType:            "my-league-type2",
		StartDate:             startDate2,
		EndDate:               endDate2,
		GameCode:              "my-game-code2",
		Season:                2023,
	}
	diffs := DiffLeague(l1, l2)
	assert.Equal(t, 17, len(diffs))
	assertDiff(t, diffs[0], AID, uint(76), uint(83))
	assertDiff(t, diffs[1], "Key", "my-key", "my-key2")
	assertDiff(t, diffs[2], AName, "my-name", "my-name2")
	assertDiff(t, diffs[3], "URL", "my-url", "my-url2")
	assertDiff(t, diffs[4], "LogoURL", "my-logo-url", "my-logo-url2")
	assertDiff(t, diffs[5], "DraftStatus", "my-draft-status", "my-draft-status2")
	assertDiff(t, diffs[6], "NumTeams", 11, 12)
	assertDiff(t, diffs[7], "EditKey", "my-edit-key", "my-edit-key2")
	assertDiff(t, diffs[8], "LeagueUpdateTimestamp", int64(123), int64(1232))
	assertDiff(t, diffs[9], "ScoringType", "my-scoring-type", "my-scoring-type2")
	assertDiff(t, diffs[10], "LeagueType", "my-league-type", "my-league-type2")
	assertDiff(t, diffs[11], "IsProLeague", true, false)
	assertDiff(t, diffs[12], "IsCashLeague", true, false)
	assertDiff(t, diffs[13], "StartDate", startDate1, startDate2)
	assertDiff(t, diffs[14], "EndDate", endDate1, endDate2)
	assertDiff(t, diffs[15], "GameCode", "my-game-code", "my-game-code2")
	assertDiff(t, diffs[16], "Season", 2022, 2023)
}
