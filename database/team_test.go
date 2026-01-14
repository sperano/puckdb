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

func TestManagerGraphQLModel(t *testing.T) {
	t.Parallel()
	manager := &Manager{
		ManagerID: 123,
		Nickname:  "my-nickname",
		GUID:      "my-guid",
		EMail:     "my-email",
		ImageURL:  "my-image-url",
	}
	gm := manager.GraphQLModel()
	assert.Equal(t, 123, gm.ManagerID)
	assert.Equal(t, "my-nickname", gm.Nickname)
	assert.Equal(t, "my-guid", gm.GUID)
	assert.Equal(t, "my-email", gm.EMail)
	assert.Equal(t, "my-image-url", gm.ImageURL)
}

func TestManagerDiffSame(t *testing.T) {
	t.Parallel()
	manager := &Manager{
		ManagerID: 123,
		Nickname:  "my-nickname",
		GUID:      "my-guid",
		EMail:     "my-email",
		ImageURL:  "my-image-url",
	}
	diffs := DiffManager(manager, manager)
	assert.Len(t, diffs, 0)
}

func TestManagerDiff(t *testing.T) {
	t.Parallel()
	m1 := &Manager{
		ManagerID: 123,
		Nickname:  "my-nickname",
		GUID:      "my-guid",
		EMail:     "my-email",
		ImageURL:  "my-image-url",
	}
	m2 := &Manager{
		ManagerID: 1232,
		Nickname:  "my-nickname2",
		GUID:      "my-guid2",
		EMail:     "my-email2",
		ImageURL:  "my-image-url2",
	}
	diffs := DiffManager(m1, m2)
	assert.Equal(t, 5, len(diffs))
	assertDiff(t, diffs[0], "ManagerID", uint(123), uint(1232))
	assertDiff(t, diffs[1], "Nickname", "my-nickname", "my-nickname2")
	assertDiff(t, diffs[2], "GUID", "my-guid", "my-guid2")
	assertDiff(t, diffs[3], "EMail", "my-email", "my-email2")
	assertDiff(t, diffs[4], "ImageURL", "my-image-url", "my-image-url2")
}

func TestTeamEnsure(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt := time.Date(2022, 9, 30, 10, 15, 26, 0, time.UTC)
	fileTS := time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC)
	team := &Team{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		Key:                   "my-key",
		Name:                  "my-name",
		IsOwnedByCurrentLogin: true,
		URL:                   "my-url",
		DraftPosition:         19,
		HasDraftGrade:         true,
		Manager: Manager{
			ManagerID: 123,
			Nickname:  "my-nickname",
			GUID:      "my-guid",
			EMail:     "my-email",
			ImageURL:  "my-image-url",
		},
		SourceVersion: fileTS,
	}
	gormdb, db, mock := sqlmockNew(t)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	stmt := fmt.Sprintf(`INSERT INTO "%s" ("%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s") `+
		`VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT ("%s") DO UPDATE SET %s RETURNING "%s"`,
		TblTeams, CCreatedAt, CUpdatedAt, CDeletedAt, CKey, CName, CIsOwnedByCurrentLogin, CURL, CDraftPosition, CHasDraftGrade,
		CManagerID, CNickname, CGUID, CEMail, CImageUL, CSourceVersion, CID, CID, upexes(teamUpdateCols), CID)
	mock.ExpectQuery(regexp.QuoteMeta(stmt)).
		WithArgs(createdAt, updatedAt, nil, "my-key", "my-name", true, "my-url", 19, true, 123, "my-nickname", "my-guid",
			"my-email", "my-image-url", fileTS, 76).
		WillReturnRows(sqlmock.NewRows([]string{CID}).AddRow(76))
	mock.ExpectCommit()

	if err := team.Ensure(gormdb); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err.Error())
	}
}

func TestTeam_SetVersion(t *testing.T) {
	t.Parallel()
	team := &Team{}
	assert.Equal(t, time.Time{}, team.SourceVersion)
	team.SetSourceVersion(time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC))
	assert.Equal(t, time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC), team.SourceVersion)
}

func TestTeamGraphQLModel(t *testing.T) {
	t.Parallel()
	team := &Team{
		Model: gorm.Model{
			ID: 76,
		},
		Key:                   "my-key",
		Name:                  "my-name",
		IsOwnedByCurrentLogin: true,
		URL:                   "my-url",
		DraftPosition:         19,
		HasDraftGrade:         true,
		Manager: Manager{
			ManagerID: 123,
			Nickname:  "my-nickname",
			GUID:      "my-guid",
			EMail:     "my-email",
			ImageURL:  "my-image-url",
		},
	}
	teamGQL := team.GraphQLModel()
	assert.Equal(t, 76, teamGQL.ID)
	assert.Equal(t, "my-key", teamGQL.Key)
	assert.Equal(t, "my-name", teamGQL.Name)
	assert.True(t, teamGQL.IsOwnedByCurrentLogin)
	assert.Equal(t, "my-url", teamGQL.URL)
	assert.Equal(t, 19, teamGQL.DraftPosition)
	assert.True(t, teamGQL.HasDraftGrade)
	assert.Equal(t, 123, teamGQL.Manager.ManagerID)
	assert.Equal(t, "my-nickname", teamGQL.Manager.Nickname)
	assert.Equal(t, "my-guid", teamGQL.Manager.GUID)
	assert.Equal(t, "my-email", teamGQL.Manager.EMail)
	assert.Equal(t, "my-image-url", teamGQL.Manager.ImageURL)
}

func TestTeamDiffSame(t *testing.T) {
	t.Parallel()
	team := &Team{
		Model: gorm.Model{
			ID: 76,
		},
		Key:                   "my-key",
		Name:                  "my-name",
		IsOwnedByCurrentLogin: true,
		URL:                   "my-url",
		DraftPosition:         19,
		HasDraftGrade:         true,
		Manager: Manager{
			ManagerID: 123,
			Nickname:  "my-nickname",
			GUID:      "my-guid",
			EMail:     "my-email",
			ImageURL:  "my-image-url",
		},
	}
	diffs := DiffTeam(team, team)
	assert.Len(t, diffs, 0)
}

func TestTeamDiff(t *testing.T) {
	t.Parallel()
	t1 := &Team{
		Model: gorm.Model{
			ID: 76,
		},
		Key:                   "my-key",
		Name:                  "my-name",
		IsOwnedByCurrentLogin: true,
		URL:                   "my-url",
		DraftPosition:         19,
		HasDraftGrade:         true,
		Manager: Manager{
			ManagerID: 123,
			Nickname:  "my-nickname",
			GUID:      "my-guid",
			EMail:     "my-email",
			ImageURL:  "my-image-url",
		},
	}
	t2 := &Team{
		Model: gorm.Model{
			ID: 83,
		},
		Key:                   "my-key2",
		Name:                  "my-name2",
		IsOwnedByCurrentLogin: false,
		URL:                   "my-url2",
		DraftPosition:         192,
		HasDraftGrade:         false,
		Manager: Manager{
			ManagerID: 1232,
			Nickname:  "my-nickname2",
			GUID:      "my-guid2",
			EMail:     "my-email2",
			ImageURL:  "my-image-url2",
		},
	}
	diffs := DiffTeam(t1, t2)
	assert.Equal(t, 12, len(diffs))
	assertDiff(t, diffs[0], AID, uint(76), uint(83))
	assertDiff(t, diffs[1], "Key", "my-key", "my-key2")
	assertDiff(t, diffs[2], AName, "my-name", "my-name2")
	assertDiff(t, diffs[3], "IsOwnedByCurrentLogin", true, false)
	assertDiff(t, diffs[4], "URL", "my-url", "my-url2")
	assertDiff(t, diffs[5], "DraftPosition", 19, 192)
	assertDiff(t, diffs[6], "HasDraftGrade", true, false)
	assertDiff(t, diffs[7], "ManagerID", uint(123), uint(1232))
	assertDiff(t, diffs[8], "Nickname", "my-nickname", "my-nickname2")
	assertDiff(t, diffs[9], "GUID", "my-guid", "my-guid2")
	assertDiff(t, diffs[10], "EMail", "my-email", "my-email2")
	assertDiff(t, diffs[11], "ImageURL", "my-image-url", "my-image-url2")
}
