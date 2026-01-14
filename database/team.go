package database

import (
	"time"

	gqlmodel "github.com/sperano/yfh/graph/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Manager struct {
	ManagerID uint
	Nickname  string
	GUID      string
	EMail     string
	ImageURL  string
}

func DiffManager(previous *Manager, current *Manager) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.ManagerID != previous.ManagerID {
		diffs = append(diffs, &Different[uint]{name: "ManagerID", previous: previous.ManagerID, current: current.ManagerID})
	}
	if current.Nickname != previous.Nickname {
		diffs = append(diffs, &Different[string]{name: "Nickname", previous: previous.Nickname, current: current.Nickname})
	}
	if current.GUID != previous.GUID {
		diffs = append(diffs, &Different[string]{name: "GUID", previous: previous.GUID, current: current.GUID})
	}
	if current.EMail != previous.EMail {
		diffs = append(diffs, &Different[string]{name: "EMail", previous: previous.EMail, current: current.EMail})
	}
	if current.ImageURL != previous.ImageURL {
		diffs = append(diffs, &Different[string]{name: "ImageURL", previous: previous.ImageURL, current: current.ImageURL})
	}
	return diffs
}

func (m *Manager) GraphQLModel() *gqlmodel.Manager {
	return &gqlmodel.Manager{
		ManagerID: int(m.ManagerID),
		Nickname:  m.Nickname,
		GUID:      m.GUID,
		EMail:     m.EMail,
		ImageURL:  m.ImageURL,
	}
}

type Team struct {
	gorm.Model
	Key                   string
	Name                  string
	IsOwnedByCurrentLogin bool
	URL                   string
	DraftPosition         int
	HasDraftGrade         bool
	Manager               Manager `gorm:"embedded"`
	SourceVersion         time.Time
}

var teamUpdateCols = []string{
	CKey,
	CName,
	CIsOwnedByCurrentLogin,
	CURL,
	CDraftPosition,
	CHasDraftGrade,
	CManagerID,
	CNickname,
	CGUID,
	CEMail,
	CImageUL,
	CSourceVersion,
}

func (t *Team) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{colID},
		DoUpdates: clause.AssignmentColumns(teamUpdateCols),
	}).Create(t).Error
}

func (t *Team) GraphQLModel() *gqlmodel.Team {
	return &gqlmodel.Team{
		ID:                    int(t.ID),
		Key:                   t.Key,
		Name:                  t.Name,
		IsOwnedByCurrentLogin: t.IsOwnedByCurrentLogin,
		URL:                   t.URL,
		DraftPosition:         t.DraftPosition,
		HasDraftGrade:         t.HasDraftGrade,
		Manager:               t.Manager.GraphQLModel(),
	}
}

func (t *Team) SetSourceVersion(time time.Time) {
	t.SourceVersion = time
}

func DiffTeam(previous *Team, current *Team) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.ID != previous.ID {
		diffs = append(diffs, &Different[uint]{name: "ID", previous: previous.ID, current: current.ID})
	}
	if current.Key != previous.Key {
		diffs = append(diffs, &Different[string]{name: "Key", previous: previous.Key, current: current.Key})
	}
	if current.Name != previous.Name {
		diffs = append(diffs, &Different[string]{name: "Name", previous: previous.Name, current: current.Name})
	}
	if current.IsOwnedByCurrentLogin != previous.IsOwnedByCurrentLogin {
		diffs = append(diffs, &Different[bool]{name: "IsOwnedByCurrentLogin", previous: previous.IsOwnedByCurrentLogin, current: current.IsOwnedByCurrentLogin})
	}
	if current.URL != previous.URL {
		diffs = append(diffs, &Different[string]{name: "URL", previous: previous.URL, current: current.URL})
	}
	if current.DraftPosition != previous.DraftPosition {
		diffs = append(diffs, &Different[int]{name: "DraftPosition", previous: previous.DraftPosition, current: current.DraftPosition})
	}
	if current.HasDraftGrade != previous.HasDraftGrade {
		diffs = append(diffs, &Different[bool]{name: "HasDraftGrade", previous: previous.HasDraftGrade, current: current.HasDraftGrade})
	}
	return append(diffs, DiffManager(&previous.Manager, &current.Manager)...)
}
