package database

import (
	"gorm.io/gorm/clause"
	"strconv"
	"time"

	gqlmodel "github.com/sperano/puckdb/graph/model"
	"gorm.io/gorm"
)

var playerUpdateCols = []string{
	CFirstName,
	CLastName,
	CUniformNumber,
	CHomeURL,
	CImageSmall,
	CImageMedium,
	CImageLarge,
	CNHLTeamID,
	CSourceVersion,
}

type Player struct {
	gorm.Model
	FirstName     string
	LastName      string
	UniformNumber int
	HomeURL       string
	ImageSmall    string
	ImageMedium   string
	ImageLarge    string
	NHLTeamID     uint
	NHLTeam       NHLTeam
	SourceVersion time.Time
}

func (p *Player) SetSourceVersion(time time.Time) {
	p.SourceVersion = time
}

type Players []*Player

func (p *Players) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: CID}},
		DoUpdates: clause.AssignmentColumns(playerUpdateCols),
	}).Create(p).Error
}

func (p *Player) GraphQLModel() *gqlmodel.Player {
	return &gqlmodel.Player{
		ID:            int(p.ID),
		FirstName:     p.FirstName,
		LastName:      p.LastName,
		UniformNumber: p.UniformNumber,
		HomeURL:       p.HomeURL,
		ImageSmall:    p.ImageSmall,
		ImageMedium:   p.ImageMedium,
		ImageLarge:    p.ImageLarge,
		NhlTeam:       p.NHLTeam.GraphQLModel(),
		CreatedAt:     p.CreatedAt,
		UpdatedAt:     p.UpdatedAt,
	}
}

func DiffPlayer(current *Player, previous *Player) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.ID != previous.ID {
		diffs = append(diffs, &Different[uint]{name: "ID", previous: previous.ID, current: current.ID})
	}
	if current.FirstName != previous.FirstName {
		diffs = append(diffs, &Different[string]{name: "FirstName", previous: previous.FirstName, current: current.FirstName})
	}
	if current.LastName != previous.LastName {
		diffs = append(diffs, &Different[string]{name: "LastName", previous: previous.LastName, current: current.LastName})
	}
	if current.UniformNumber != previous.UniformNumber {
		diffs = append(diffs, &Different[int]{name: "UniformNumber", previous: previous.UniformNumber, current: current.UniformNumber})
	}
	if current.HomeURL != previous.HomeURL {
		diffs = append(diffs, &Different[string]{name: "HomeURL", previous: previous.HomeURL, current: current.HomeURL})
	}
	if current.ImageSmall != previous.ImageSmall {
		diffs = append(diffs, &Different[string]{name: "ImageSmall", previous: previous.ImageSmall, current: current.ImageSmall})
	}
	if current.ImageMedium != previous.ImageMedium {
		diffs = append(diffs, &Different[string]{name: "ImageMedium", previous: previous.ImageMedium, current: current.ImageMedium})
	}
	if current.ImageLarge != previous.ImageLarge {
		diffs = append(diffs, &Different[string]{name: "ImageLarge", previous: previous.ImageLarge, current: current.ImageLarge})
	}
	return append(diffs, DiffNHLTeam(&current.NHLTeam, &previous.NHLTeam)...)
}

var playerStatsUpdateCols = []string{
	CNHLTeamID,
	CGoalAgainst,
	CShotsAgainst,
	CSaves,
	CGoalieTimeOnIce,
	CGoals,
	CAssists,
	CPlusMinus,
	CPenaltyMinutes,
	CShotsOnGoal,
	CFaceoffsWon,
	CFaceoffsLost,
	CHits,
	CBlocks,
	CTimeOnIce,
	CShifts,
	CTakeAways,
	CGiveAways,
	CSourceVersion,
}

type StatsSkater struct {
	Goals          uint
	Assists        uint
	PlusMinus      int
	PenaltyMinutes uint
	ShotsOnGoal    uint
	FaceoffsWon    uint
	FaceoffsLost   uint
	Hits           uint
	Blocks         uint
	TimeOnIce      uint
	Shifts         uint
	TakeAways      uint
	GiveAways      uint
}

type StatsGoaler struct {
	GoalAgainst     uint
	ShotsAgainst    uint
	Saves           uint
	GoalieTimeOnIce uint
}

type PlayerStats struct {
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
	Date          time.Time      `gorm:"primaryKey"`
	PlayerID      uint           `gorm:"primaryKey"`
	SourceVersion time.Time
	NHLTeamID     uint `gorm:"index"`
	NHLTeam       NHLTeam
	StatsGoaler
	StatsSkater
}

func (ps *PlayerStats) SetSourceVersion(time time.Time) {
	ps.SourceVersion = time
}

func DiffStatsGoaler(current *StatsGoaler, previous *StatsGoaler) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.GoalAgainst != previous.GoalAgainst {
		diffs = append(diffs, &Different[uint]{name: "GoalAgainst", previous: previous.GoalAgainst, current: current.GoalAgainst})
	}
	if current.ShotsAgainst != previous.ShotsAgainst {
		diffs = append(diffs, &Different[uint]{name: "ShotsAgainst", previous: previous.ShotsAgainst, current: current.ShotsAgainst})
	}
	if current.Saves != previous.Saves {
		diffs = append(diffs, &Different[uint]{name: "Saves", previous: previous.Saves, current: current.Saves})
	}
	if current.GoalieTimeOnIce != previous.GoalieTimeOnIce {
		diffs = append(diffs, &Different[uint]{name: "GoalieTimeOnIce", previous: previous.GoalieTimeOnIce, current: current.GoalieTimeOnIce})
	}
	return diffs
}

func DiffStatsSkater(current *StatsSkater, previous *StatsSkater) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.Goals != previous.Goals {
		diffs = append(diffs, &Different[uint]{name: "Goals", previous: previous.Goals, current: current.Goals})
	}
	if current.Assists != previous.Assists {
		diffs = append(diffs, &Different[uint]{name: "Assists", previous: previous.Assists, current: current.Assists})
	}
	if current.PlusMinus != previous.PlusMinus {
		diffs = append(diffs, &Different[int]{name: "PlusMinus", previous: previous.PlusMinus, current: current.PlusMinus})
	}
	if current.PenaltyMinutes != previous.PenaltyMinutes {
		diffs = append(diffs, &Different[uint]{name: "PenaltyMinutes", previous: previous.PenaltyMinutes, current: current.PenaltyMinutes})
	}
	if current.ShotsOnGoal != previous.ShotsOnGoal {
		diffs = append(diffs, &Different[uint]{name: "ShotsOnGoal", previous: previous.ShotsOnGoal, current: current.ShotsOnGoal})
	}
	if current.FaceoffsWon != previous.FaceoffsWon {
		diffs = append(diffs, &Different[uint]{name: "FaceoffsWon", previous: previous.FaceoffsWon, current: current.FaceoffsWon})
	}
	if current.FaceoffsLost != previous.FaceoffsLost {
		diffs = append(diffs, &Different[uint]{name: "FaceoffsLost", previous: previous.FaceoffsLost, current: current.FaceoffsLost})
	}
	if current.Hits != previous.Hits {
		diffs = append(diffs, &Different[uint]{name: "Hits", previous: previous.Hits, current: current.Hits})
	}
	if current.Blocks != previous.Blocks {
		diffs = append(diffs, &Different[uint]{name: "Blocks", previous: previous.Blocks, current: current.Blocks})
	}
	if current.TimeOnIce != previous.TimeOnIce {
		diffs = append(diffs, &Different[uint]{name: "TimeOnIce", previous: previous.TimeOnIce, current: current.TimeOnIce})
	}
	if current.Shifts != previous.Shifts {
		diffs = append(diffs, &Different[uint]{name: "Shifts", previous: previous.Shifts, current: current.Shifts})
	}
	if current.TakeAways != previous.TakeAways {
		diffs = append(diffs, &Different[uint]{name: "TakeAways", previous: previous.TakeAways, current: current.TakeAways})
	}
	if current.GiveAways != previous.GiveAways {
		diffs = append(diffs, &Different[uint]{name: "GiveAways", previous: previous.GiveAways, current: current.GiveAways})
	}
	return diffs
}

func DiffPlayerStats(current *PlayerStats, previous *PlayerStats) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.Date != previous.Date {
		diffs = append(diffs, &Different[time.Time]{name: "Date", previous: previous.Date, current: current.Date})
	}
	if current.PlayerID != previous.PlayerID {
		diffs = append(diffs, &Different[uint]{name: "PlayerID", previous: previous.PlayerID, current: current.PlayerID})
	}
	if current.NHLTeamID != previous.NHLTeamID {
		diffs = append(diffs, &Different[uint]{name: "NHLTeamID", previous: previous.NHLTeamID, current: current.NHLTeamID})
	}
	diffs = append(diffs, DiffStatsGoaler(&current.StatsGoaler, &previous.StatsGoaler)...)
	return append(diffs, DiffStatsSkater(&current.StatsSkater, &previous.StatsSkater)...)
}

// TODO would pointer work with temporal activities? double check that....
type PlayersStats []*PlayerStats

func (ps *PlayersStats) Ensure(db *gorm.DB) error {
	err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "date"}, {Name: "player_id"}}, // TODO constants
		DoUpdates: clause.AssignmentColumns(playerStatsUpdateCols),
	}).Create(ps).Error
	return err
}

func TrySetStatUInt(ptr *uint, str string) error {
	if len(str) > 0 {
		if i, err := strconv.Atoi(str); err != nil {
			return err
		} else {
			*ptr = uint(i)
		}
	}
	return nil
}

func TrySetStatInt(ptr *int, str string) error {
	if len(str) > 0 {
		if i, err := strconv.Atoi(str); err != nil {
			return err
		} else {
			*ptr = i
		}
	}
	return nil
}

//func LogPlayerStats(date time.Time, playerStats *PlayerStats) {
//	log.Debug().
//		Str("date", fmt.Sprintf("%d-%02d-%02d", date.Year(), date.Month(), date.Day())).
//		Uint("player_id", playerStats.PlayerID).
//		Uint("nhl_team_id", playerStats.NHLTeamID).
//		Uint("ga", playerStats.GoalAgainst).
//		Uint("sa", playerStats.ShotsAgainst).
//		Uint("sv", playerStats.Saves).
//		Uint("gtoi", playerStats.GoalieTimeOnIce).
//		Uint("g", playerStats.Goals).
//		Uint("a", playerStats.Assists).
//		Int("+-", playerStats.PlusMinus).
//		Uint("pim", playerStats.PenaltyMinutes).
//		Uint("sog", playerStats.ShotsOnGoal).
//		Uint("fw", playerStats.FaceoffsWon).
//		Uint("fl", playerStats.FaceoffsLost).
//		Uint("h", playerStats.Hits).
//		Uint("b", playerStats.Blocks).
//		Uint("toi", playerStats.TimeOnIce).
//		Uint("sh", playerStats.Shifts).
//		Uint("taw", playerStats.TakeAways).
//		Uint("gaw", playerStats.GiveAways).
//		Msg("Ensuring player stats")
//}
