package database

import (
	"fmt"
	gqlmodel "github.com/sperano/yfh/graph/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

type StatGroup int

const (
	Offense StatGroup = iota
	Goaltending
)

var statGroups = map[string]StatGroup{
	"offense":     Offense,
	"goaltending": Goaltending,
}

func ScanStatGroup(data string) (StatGroup, error) {
	sg, ok := statGroups[data]
	if !ok {
		// TODO return -1 ?
		return 0, fmt.Errorf("invalid stat group: %s", data)
	}
	return sg, nil
}

type StatDefinition struct {
	gorm.Model
	Enabled bool
	Name    string
	Abbr    string
	Group   StatGroup `gorm:"type:int"`
}

type RosterPosition struct {
	gorm.Model
	Position           PositionName `gorm:"type:int"`
	PositionType       PositionType `gorm:"type:int"`
	Count              int
	IsStartingPosition bool
}

type League struct {
	gorm.Model
	Key                   string
	Name                  string
	URL                   string
	LogoURL               string
	DraftStatus           string
	NumTeams              int
	EditKey               string
	LeagueUpdateTimestamp int64
	ScoringType           string
	LeagueType            string
	IsProLeague           bool
	IsCashLeague          bool
	StartDate             time.Time
	EndDate               time.Time
	GameCode              string
	Season                int
	DraftType             string
	IsAuctionDraft        bool
	PersistentURL         string
	UsesPlayoff           bool
	WaiverType            string
	WaiverRule            string
	DraftTime             int
	DraftPickTime         int
	PostDraftPlayers      string
	MaxTeams              int
	WaiverTime            int
	TradeEndDate          time.Time
	TradeRatifyType       string
	TradeRejectTime       int
	PlayerPool            string
	CantCutList           string
	//DraftTogether      int
	SendbirdChannelURL string
	StatsDefinitions   []*StatDefinition `gorm:"many2many:league_stat_definitions;"`
	RosterPositions    []*RosterPosition `gorm:"many2many:league_roster_positions;"`
	SourceVersion      time.Time
}

var leagueUpdateCols = []string{
	CKey,
	CName,
	CURL,
	CLogoURL,
	CDraftStatus,
	CNumTeams,
	CEditKey,
	CLeagueUpdateTimestamp,
	CScoringType,
	CLeagueType,
	CIsProLeague,
	CIsCashLeague,
	CStartDate,
	CEndDate,
	CGameCode,
	CSeason,
	CDraftType,
	CIsAuctionDraft,
	CPersistentURL,
	CUsesPlayoff,
	CWaiverType,
	CWaiverRule,
	CDraftTime,
	CDrafTPickTime,
	CPostDraftPlayers,
	CMaxTeams,
	CWaiverTime,
	CTradeEndDate,
	CTradeRatifyType,
	CTradeRejectTime,
	CPlayerPool,
	CCantCutList,
	CSendbirdChannelURL,
	CSourceVersion,
}

func (l *League) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{colID},
		DoUpdates: clause.AssignmentColumns(leagueUpdateCols),
	}).Create(l).Error
}

func (l *League) GraphQLModel() *gqlmodel.League {
	return &gqlmodel.League{
		ID:                    int(l.ID),
		Key:                   l.Key,
		Name:                  l.Name,
		URL:                   l.URL,
		LogoURL:               l.LogoURL,
		DraftStatus:           l.DraftStatus,
		NumTeams:              l.NumTeams,
		EditKey:               l.EditKey,
		LeagueUpdateTimestamp: strconv.FormatInt(l.LeagueUpdateTimestamp, 10),
		ScoringType:           l.ScoringType,
		LeagueType:            l.LeagueType,
		IsProLeague:           l.IsProLeague,
		IsCashLeague:          l.IsCashLeague,
		StartDate:             l.StartDate,
		EndDate:               l.EndDate,
		GameCode:              l.GameCode,
		Season:                l.Season,
		CreatedAt:             l.CreatedAt,
		UpdatedAt:             l.UpdatedAt,
	}
}

func (l *League) SetSourceVersion(time time.Time) {
	l.SourceVersion = time
}

func DiffLeague(previous *League, current *League) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.ID != previous.ID {
		diffs = append(diffs, &Different[uint]{name: AID, previous: previous.ID, current: current.ID})
	}
	if current.Key != previous.Key {
		diffs = append(diffs, &Different[string]{name: "Key", previous: previous.Key, current: current.Key})
	}
	if current.Name != previous.Name {
		diffs = append(diffs, &Different[string]{name: "Name", previous: previous.Name, current: current.Name})
	}
	if current.URL != previous.URL {
		diffs = append(diffs, &Different[string]{name: "URL", previous: previous.URL, current: current.URL})
	}
	if current.LogoURL != previous.LogoURL {
		diffs = append(diffs, &Different[string]{name: "LogoURL", previous: previous.LogoURL, current: current.LogoURL})
	}
	if current.DraftStatus != previous.DraftStatus {
		diffs = append(diffs, &Different[string]{name: "DraftStatus", previous: previous.DraftStatus, current: current.DraftStatus})
	}
	if current.NumTeams != previous.NumTeams {
		diffs = append(diffs, &Different[int]{name: "NumTeams", previous: previous.NumTeams, current: current.NumTeams})
	}
	if current.EditKey != previous.EditKey {
		diffs = append(diffs, &Different[string]{name: "EditKey", previous: previous.EditKey, current: current.EditKey})
	}
	if current.LeagueUpdateTimestamp != previous.LeagueUpdateTimestamp {
		diffs = append(diffs, &Different[int64]{name: "LeagueUpdateTimestamp", previous: previous.LeagueUpdateTimestamp, current: current.LeagueUpdateTimestamp})
	}
	if current.ScoringType != previous.ScoringType {
		diffs = append(diffs, &Different[string]{name: "ScoringType", previous: previous.ScoringType, current: current.ScoringType})
	}
	if current.LeagueType != previous.LeagueType {
		diffs = append(diffs, &Different[string]{name: "LeagueType", previous: previous.LeagueType, current: current.LeagueType})
	}
	if current.IsProLeague != previous.IsProLeague {
		diffs = append(diffs, &Different[bool]{name: "IsProLeague", previous: previous.IsProLeague, current: current.IsProLeague})
	}
	if current.IsCashLeague != previous.IsCashLeague {
		diffs = append(diffs, &Different[bool]{name: "IsCashLeague", previous: previous.IsCashLeague, current: current.IsCashLeague})
	}
	if current.StartDate != previous.StartDate {
		diffs = append(diffs, &Different[time.Time]{name: "StartDate", previous: previous.StartDate, current: current.StartDate})
	}
	if current.EndDate != previous.EndDate {
		diffs = append(diffs, &Different[time.Time]{name: "EndDate", previous: previous.EndDate, current: current.EndDate})
	}
	if current.GameCode != previous.GameCode {
		diffs = append(diffs, &Different[string]{name: "GameCode", previous: previous.GameCode, current: current.GameCode})
	}
	if current.Season != previous.Season {
		diffs = append(diffs, &Different[int]{name: "Season", previous: previous.Season, current: current.Season})
	}
	if current.SourceVersion != previous.SourceVersion {
		diffs = append(diffs, &Different[time.Time]{name: "SourceVersion", previous: previous.SourceVersion, current: current.SourceVersion})
	}
	// TODO diff stats and positions
	return diffs
}
