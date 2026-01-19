package database

import (
	"time"

	gqlmodel "github.com/sperano/puckdb/graph/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const GamePostponedState = "postponed"

// Column names
const (
	CHomeTeamID      = "home_team_id"
	CHomeTeamScore1  = "home_team_score1"
	CHomeTeamScore2  = "home_team_score2"
	CHomeTeamScore3  = "home_team_score3"
	CHomeTeamScoreO  = "home_team_score_o"
	CHomeTeamScoreSO = "home_team_score_so"
	CAwayTeamID      = "away_team_id"
	CAwayTeamScore1  = "away_team_score1"
	CAwayTeamScore2  = "away_team_score2"
	CAwayTeamScore3  = "away_team_score3"
	CAwayTeamScoreO  = "away_team_score_o"
	CAwayTeamScoreSO = "away_team_score_so"
	CSourceVersion   = "source_version"
)

type Game struct {
	gorm.Model
	Date            time.Time `json:"date" gorm:"index"`
	HomeTeamID      uint      `json:"home_team_id" gorm:"index"`
	HomeTeam        NHLTeam   `json:"home_team"`
	HomeTeamScore1  int       `json:"home_team_score_1"`
	HomeTeamScore2  int       `json:"home_team_score_2"`
	HomeTeamScore3  int       `json:"home_team_score_3"`
	HomeTeamScoreO  int       `json:"home_team_score_o"`
	HomeTeamScoreSO int       `json:"home_team_score_so"`
	AwayTeamID      uint      `json:"away_team_id" gorm:"index"`
	AwayTeam        NHLTeam   `json:"away_team"`
	AwayTeamScore1  int       `json:"away_team_score_1"`
	AwayTeamScore2  int       `json:"away_team_score_2"`
	AwayTeamScore3  int       `json:"away_team_score_3"`
	AwayTeamScoreO  int       `json:"away_team_score_o"`
	AwayTeamScoreSO int       `json:"away_team_score_so"`
	State           string    `json:"state"`
	SourceVersion   time.Time `json:"source_version"`
}

func (g *Game) SetSourceVersion(time time.Time) {
	g.SourceVersion = time
}

func (g *Game) IsPostPoned() bool {
	return g.State == GamePostponedState
}

func (g *Game) IsOvertime() bool {
	return g.AwayTeamScoreO+g.HomeTeamScoreO > 0
}

func (g *Game) IsShootout() bool {
	return g.HomeTeamScoreSO+g.HomeTeamScoreSO > 0
}

func (g *Game) AwayTeamScore() int {
	return g.CountingAwayTeamScore() + g.AwayTeamScoreSO
}

func (g *Game) CountingAwayTeamScore() int {
	return g.AwayTeamScore1 + g.AwayTeamScore2 + g.AwayTeamScore3 + g.AwayTeamScoreO
}

func (g *Game) HomeTeamScore() int {
	return g.CountingHomeTeamScore() + g.HomeTeamScoreSO
}

func (g *Game) CountingHomeTeamScore() int {
	return g.HomeTeamScore1 + g.HomeTeamScore2 + g.HomeTeamScore3 + g.HomeTeamScoreO
}

func (g *Game) HomeTeamWins() bool {
	return g.HomeTeamScore() > g.AwayTeamScore()
}

var gameUpdateCols = []string{
	CDate,
	CHomeTeamID,
	CHomeTeamScore1,
	CHomeTeamScore2,
	CHomeTeamScore3,
	CHomeTeamScoreO,
	CHomeTeamScoreSO,
	CAwayTeamID,
	CAwayTeamScore1,
	CAwayTeamScore2,
	CAwayTeamScore3,
	CAwayTeamScoreO,
	CAwayTeamScoreSO,
	CState,
	CSourceVersion,
}

func (g *Game) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{colID},
		DoUpdates: clause.AssignmentColumns(gameUpdateCols),
	}).Create(g).Error
}

func (g *Game) GraphQLModel() *gqlmodel.Game {
	return &gqlmodel.Game{
		ID:              int(g.ID),
		Date:            g.Date,
		HomeTeam:        g.HomeTeam.GraphQLModel(),
		HomeTeamScore1:  g.HomeTeamScore1,
		HomeTeamScore2:  g.HomeTeamScore2,
		HomeTeamScore3:  g.HomeTeamScore3,
		HomeTeamScoreO:  g.HomeTeamScoreO,
		HomeTeamScoreSo: g.HomeTeamScoreSO,
		AwayTeam:        g.AwayTeam.GraphQLModel(),
		AwayTeamScore1:  g.AwayTeamScore1,
		AwayTeamScore2:  g.AwayTeamScore2,
		AwayTeamScore3:  g.AwayTeamScore3,
		AwayTeamScoreO:  g.AwayTeamScoreO,
		AwayTeamScoreSo: g.AwayTeamScoreSO,
		State:           g.State,
		CreatedAt:       g.CreatedAt,
		UpdatedAt:       g.UpdatedAt,
	}
}

func DiffGameLinks(previous []string, current []string) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	for i := 0; i < min(len(current), len(previous)); i++ {
		if previous[i] != current[i] {
			diffs = append(diffs, &Different[string]{name: "GameLink", previous: previous[i], current: current[i]})
		}
	}
	if len(previous) < len(current) {
		for i := len(previous); i < len(current); i++ {
			diffs = append(diffs, &Different[string]{name: "GameLink", previous: "", current: current[i]})
		}
	}
	if len(previous) > len(current) {
		for i := len(current); i < len(previous); i++ {
			diffs = append(diffs, &Different[string]{name: "GameLink", previous: previous[i], current: ""})
		}
	}
	return diffs
}

func DiffGame(current *Game, previous *Game) []DifferentAttr {
	//reflect.ValueOf()
	// TODO more reflection!
	diffs := make([]DifferentAttr, 0)
	if current.ID != previous.ID {
		diffs = append(diffs, &Different[uint]{name: getaname(current.Model, 0), previous: previous.ID, current: current.ID})
	}
	if current.Date != previous.Date {
		diffs = append(diffs, &Different[time.Time]{name: getaname(current, 1), previous: previous.Date, current: current.Date})
	}
	if current.HomeTeamID != previous.HomeTeamID {
		diffs = append(diffs, &Different[uint]{name: getaname(current, 2), previous: previous.HomeTeamID, current: current.HomeTeamID})
	}
	if current.HomeTeamScore1 != previous.HomeTeamScore1 {
		diffs = append(diffs, &Different[int]{name: getaname(current, 4), previous: previous.HomeTeamScore1, current: current.HomeTeamScore1})
	}
	if current.HomeTeamScore2 != previous.HomeTeamScore2 {
		diffs = append(diffs, &Different[int]{name: getaname(current, 5), previous: previous.HomeTeamScore2, current: current.HomeTeamScore2})
	}
	if current.HomeTeamScore3 != previous.HomeTeamScore3 {
		diffs = append(diffs, &Different[int]{name: getaname(current, 6), previous: previous.HomeTeamScore3, current: current.HomeTeamScore3})
	}
	if current.HomeTeamScoreO != previous.HomeTeamScoreO {
		diffs = append(diffs, &Different[int]{name: getaname(current, 7), previous: previous.HomeTeamScoreO, current: current.HomeTeamScoreO})
	}
	if current.HomeTeamScoreSO != previous.HomeTeamScoreSO {
		diffs = append(diffs, &Different[int]{name: getaname(current, 8), previous: previous.HomeTeamScoreSO, current: current.HomeTeamScoreSO})
	}
	if current.AwayTeamID != previous.AwayTeamID {
		diffs = append(diffs, &Different[uint]{name: getaname(current, 9), previous: previous.AwayTeamID, current: current.AwayTeamID})
	}
	if current.AwayTeamScore1 != previous.AwayTeamScore1 {
		diffs = append(diffs, &Different[int]{name: getaname(current, 11), previous: previous.AwayTeamScore1, current: current.AwayTeamScore1})
	}
	if current.AwayTeamScore2 != previous.AwayTeamScore2 {
		diffs = append(diffs, &Different[int]{name: getaname(current, 12), previous: previous.AwayTeamScore2, current: current.AwayTeamScore2})
	}
	if current.AwayTeamScore3 != previous.AwayTeamScore3 {
		diffs = append(diffs, &Different[int]{name: getaname(current, 13), previous: previous.AwayTeamScore3, current: current.AwayTeamScore3})
	}
	if current.AwayTeamScoreO != previous.AwayTeamScoreO {
		diffs = append(diffs, &Different[int]{name: getaname(current, 14), previous: previous.AwayTeamScoreO, current: current.AwayTeamScoreO})
	}
	if current.AwayTeamScoreSO != previous.AwayTeamScoreSO {
		diffs = append(diffs, &Different[int]{name: getaname(current, 15), previous: previous.AwayTeamScoreSO, current: current.AwayTeamScoreSO})
	}
	if current.State != previous.State {
		diffs = append(diffs, &Different[string]{name: getaname(current, 16), previous: previous.State, current: current.State})
	}
	if current.SourceVersion != previous.SourceVersion {
		//reflect.Indirect(reflect.ValueOf(q)).Type().Field(0).Name
		diffs = append(diffs, &Different[time.Time]{name: getaname(current, 17), previous: previous.SourceVersion, current: current.SourceVersion})
	}
	return diffs
}
