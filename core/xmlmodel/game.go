package xmlmodel

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ericsperano/yfh/core/model"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// TODO not really an xml, it's parsed from html differently

const GamePostPoned = "postponed"

/*
type GameLink struct {
	URL string
}
*/

type PlayerID string

func (p PlayerID) Id() (uint, error) {
	tokens := strings.Split(string(p), ".")
	if len(tokens) != 3 {
		return 0, fmt.Errorf("invalid PlayerID: %s", p)
	}
	id, err := strconv.Atoi(tokens[2])
	if err != nil {
		return 0, err
	}
	return uint(id), err
}

type GameID string

func (g GameID) Id() (uint, error) {
	tokens := strings.Split(string(g), ".")
	if len(tokens) != 3 {
		return 0, fmt.Errorf("invalid GameID: %s", g)
	}
	id, err := strconv.Atoi(tokens[2])
	if err != nil {
		return 0, err
	}
	return uint(id), err
}

type PositionID string

func (p PositionID) Id() (uint, error) {
	tokens := strings.Split(string(p), ".")
	if len(tokens) != 3 {
		return 0, fmt.Errorf("invalid PositionID: %s", p)
	}
	id, err := strconv.Atoi(tokens[2])
	if err != nil {
		return 0, err
	}
	return uint(id), err
}

type NHLTeamID string

func (t NHLTeamID) Id() (uint, error) {
	tokens := strings.Split(string(t), ".")
	if len(tokens) != 3 {
		return 0, fmt.Errorf("invalid NHLTeamID: %s", t)
	}
	id, err := strconv.Atoi(tokens[2])
	if err != nil {
		return 0, err
	}
	return uint(id), err
}

type PlayerInfo struct {
	PlayerID          PlayerID   `json:"player_id"`
	FirstName         string     `json:"first_name"`
	LastName          string     `json:"last_name"`
	UniformNumber     string     `json:"uniform_number"`
	PrimaryPositionID PositionID `json:"primary_position_id"`
	NHLTeam           NHLTeamID  `json:"team_id"`
	HomeURL           string     `json:"home_url"`
	ImageSmall        string     `json:"image_small"`
	ImageMedium       string     `json:"image_medium"`
	ImageLarge        string     `json:"image_large"`
}

func (p *PlayerInfo) ToPlayerModel() (*model.Player, error) {
	id, err := p.PlayerID.Id()
	if err != nil {
		return nil, err
	}
	uni := -1
	if len(p.UniformNumber) > 0 {
		uni, err = strconv.Atoi(p.UniformNumber)
		if err != nil {
			return nil, err
		}
	} else {
		log.Warnf("Empty uniform number for %s %s\n", p.FirstName, p.LastName)
	}
	nid, err := p.NHLTeam.Id()
	if err != nil {
		return nil, err
	}
	player := model.Player{
		Model: gorm.Model{
			ID: id,
		},
		FirstName:     p.FirstName,
		LastName:      p.LastName,
		ImageSmall:    p.ImageSmall,
		ImageMedium:   p.ImageMedium,
		ImageLarge:    p.ImageLarge,
		UniformNumber: uni,
		HomeURL:       p.HomeURL,
		NHLTeamID:     nid,
	}

	return &player, nil
}

type PlayersStore struct {
	Players map[PlayerID]PlayerInfo `json:"players"`
}

type PlayerStats struct {
	PlayerID          PlayerID
	NHLTeamID         NHLTeamID
	GoalAgainst       string `json:"nhl.stat_type.22"`
	ShotsAgainst      string `json:"nhl.stat_type.24"`
	Saves             string `json:"nhl.stat_type.25"`
	SavePercentage    string `json:"nhl.stat_type.26"`
	GoalieTimeOnIce   string `json:"nhl.stat_type.52"`
	Goals             string `json:"nhl.stat_type.102"`
	Assists           string `json:"nhl.stat_type.103"`
	PlusMinus         string `json:"nhl.stat_type.104"`
	PenaltyMinutes    string `json:"nhl.stat_type.105"`
	ShotsOnGoal       string `json:"nhl.stat_type.112"`
	FaceoffsWon       string `json:"nhl.stat_type.113"`
	FaceoffsLost      string `json:"nhl.stat_type.114"`
	Points            string `json:"nhl.stat_type.115"`
	Hits              string `json:"nhl.stat_type.117"`
	Blocks            string `json:"nhl.stat_type.118"`
	TimeOnIce         string `json:"nhl.stat_type.119"`
	FaceOffPercentage string `json:"nhl.stat_type.120"`
	Shifts            string `json:"nhl.stat_type.121"`
	TakeAways         string `json:"nhl.stat_type.122"`
	GiveAways         string `json:"nhl.stat_type.123"`
}

func (ps *PlayerStats) ToPlayerStatsModel() (*model.PlayerStats, error) {
	log.Debugf("player stats source=%+v", ps)

	playerStats := &model.PlayerStats{}
	id, err := ps.PlayerID.Id()
	if err != nil {
		return nil, err
	}
	playerStats.PlayerID = id
	id, err = ps.NHLTeamID.Id()
	if err != nil {
		return nil, err
	}
	playerStats.NHLTeamID = id

	if err := model.MaybeSetStatUInt(&playerStats.GoalAgainst, ps.GoalAgainst); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.ShotsAgainst, ps.ShotsAgainst); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.Saves, ps.Saves); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatPercentage(&playerStats.SavePercentage, ps.SavePercentage); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.GoalieTimeOnIce, ps.GoalieTimeOnIce); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.Goals, ps.GoalieTimeOnIce); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.Assists, ps.GoalieTimeOnIce); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatInt(&playerStats.PlusMinus, ps.PlusMinus); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.PenaltyMinutes, ps.PenaltyMinutes); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.ShotsOnGoal, ps.ShotsOnGoal); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.FaceoffsWon, ps.FaceoffsWon); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.FaceoffsLost, ps.FaceoffsLost); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.Hits, ps.Hits); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.Blocks, ps.Blocks); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.TimeOnIce, ps.TimeOnIce); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatPercentage(&playerStats.FaceOffPercentage, ps.FaceOffPercentage); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.Shifts, ps.Shifts); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.TakeAways, ps.TakeAways); err != nil {
		return nil, err
	}
	if err := model.MaybeSetStatUInt(&playerStats.GiveAways, ps.GiveAways); err != nil {
		return nil, err
	}
	return playerStats, nil
}

type PlayerStatsContainer struct {
	Stats PlayerStats `json:"nhl.stat_variation.2"`
}

type StatsStore struct {
	PlayerStats map[PlayerID]PlayerStatsContainer `json:"playerStats"`
}

type GameLineupEntry struct {
	Class    string   `json:"class"`
	PlayerID PlayerID `json:"player_id"`
	Order    int      `json:"order"`
	Captain  string   `json:"captain"`
	Starter  string   `json:"starter"`
}

type GameLineup struct {
	All map[string]GameLineupEntry `json:"all"`
}

type GameLineups struct {
	HomeLineup GameLineup `json:"home_lineup"`
	AwayLineup GameLineup `json:"away_lineup"`
}

type GameDetails struct {
	GameType        string      `json:"game_type"` // "All-Star", "Regular Season"
	Lineups         GameLineups `json:"lineups"`
	TotalHomePoints string      `json:"total_home_points"`
	TotalAwayPoints string      `json:"total_away_points"`
}

type TeamInfo struct {
	TeamID       NHLTeamID `json:"team_id"`
	FirstName    string    `json:"first_name"`
	LastName     string    `json:"last_name"`
	Division     string    `json:"division"`
	DivisionID   string    `json:"division_id"`
	Conference   string    `json:"conference"`
	ConferenceID string    `json:"conference_id"`
	HomeLink     string    `json:"team_home_link"`
	ScheduleLink string    `json:"team_schedule_link"`
}

func (t *TeamInfo) ToNHLTeamModel() (*model.NHLTeam, error) {
	id, err := t.TeamID.Id()
	if err != nil {
		return nil, err
	}
	divisionID, err := strconv.Atoi(t.DivisionID)
	if err != nil {
		return nil, err
	}
	confID, err := strconv.Atoi(t.ConferenceID)
	if err != nil {
		return nil, err
	}
	team := &model.NHLTeam{
		Model: gorm.Model{
			ID: id,
		},
		NHLDivisionID:   uint(divisionID),
		NHLConferenceID: uint(confID),
		City:            t.FirstName,
		Name:            t.LastName,
		HomeLink:        t.HomeLink,
		ScheduleLink:    t.ScheduleLink,
	}
	return team, nil
}

type TeamsHolder struct {
	Teams map[NHLTeamID]TeamInfo `json:"teams"`
}
type GamesStore struct {
	Games   map[GameID]GameDetails `json:"games"`
	Records map[GameID]TeamsHolder `json:"records"`
}

type GamePageEntityData struct {
	HomeTeamID NHLTeamID `json:"homeTeamId"`
	AwayTeamID NHLTeamID `json:"awayTeamId"`
	State      string    `json:"gameState"`
}

type GamePageData struct {
	GameID     GameID             `json:"entityId"`
	EntityData GamePageEntityData `json:"entityData"`
}

type GamePageStore struct {
	PageData GamePageData `json:"pageData"`
}

type GameStores struct {
	PageStore    GamePageStore `json:"PageStore"`
	GamesStore   GamesStore    `json:"GamesStore"`
	PlayersStore PlayersStore  `json:"PlayersStore"`
	StatsStore   StatsStore    `json:"StatsStore"`
}

type GameDispatcher struct {
	Stores GameStores `json:"stores"`
}

type GameContext struct {
	Dispatcher GameDispatcher `json:"dispatcher"`
}

type Game struct {
	Context GameContext `json:"context"`
}

func (g *Game) IsRegularSeason() bool {
	stores := g.Context.Dispatcher.Stores
	pd := stores.PageStore.PageData
	return stores.GamesStore.Games[pd.GameID].GameType == "Regular Season"
}

func (g *Game) HomeTeam() *TeamInfo {
	stores := g.Context.Dispatcher.Stores
	pd := stores.PageStore.PageData
	t := stores.GamesStore.Records[pd.GameID].Teams[pd.EntityData.HomeTeamID]
	return &t
}

func (g *Game) HomeTeamScore() (int, error) {
	stores := g.Context.Dispatcher.Stores
	pd := stores.PageStore.PageData
	return strconv.Atoi(stores.GamesStore.Games[pd.GameID].TotalHomePoints)
}

func (g *Game) Players() []PlayerInfo {
	pis := []PlayerInfo{}
	for _, pi := range g.Context.Dispatcher.Stores.PlayersStore.Players {
		pis = append(pis, pi)
	}
	return pis
}

func (g *Game) PlayerStats() ([]PlayerStats, error) {
	psMaps := g.Context.Dispatcher.Stores.StatsStore.PlayerStats
	pls := []PlayerStats{}
	for _, pi := range g.Context.Dispatcher.Stores.PlayersStore.Players {
		ps := psMaps[pi.PlayerID]
		ps.Stats.PlayerID = pi.PlayerID
		ps.Stats.NHLTeamID = pi.NHLTeam
		pls = append(pls, ps.Stats)
	}
	return pls, nil
}

func (g *Game) AwayTeam() *TeamInfo {
	stores := g.Context.Dispatcher.Stores
	pd := stores.PageStore.PageData
	t := stores.GamesStore.Records[pd.GameID].Teams[pd.EntityData.AwayTeamID]
	return &t
}
func (g *Game) AwayTeamScore() (int, error) {
	stores := g.Context.Dispatcher.Stores
	pd := stores.PageStore.PageData
	return strconv.Atoi(stores.GamesStore.Games[pd.GameID].TotalAwayPoints)
}

func (g *Game) IsPostponed() bool {
	return g.Context.Dispatcher.Stores.PageStore.PageData.EntityData.State == GamePostPoned
}

func (g *Game) GetState() string {
	return g.Context.Dispatcher.Stores.PageStore.PageData.EntityData.State
}
