package database

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"reflect"
	"time"
)

// Column names
const (
	CID                    = "id"
	CDate                  = "date"
	CName                  = "name"
	CCreatedAt             = "created_at"
	CUpdatedAt             = "updated_at"
	CDeletedAt             = "deleted_at"
	CPlayerID              = "player_id"
	CNHLTeamID             = "nhl_team_id"
	CGoalAgainst           = "goal_against"
	CShotsAgainst          = "shots_against"
	CSaves                 = "saves"
	CGoalieTimeOnIce       = "goalie_time_on_ice"
	CGoals                 = "goals"
	CAssists               = "assists"
	CPlusMinus             = "plus_minus"
	CPenaltyMinutes        = "penalty_minutes"
	CShotsOnGoal           = "shots_on_goal"
	CFaceoffsWon           = "faceoffs_won"
	CFaceoffsLost          = "faceoffs_lost"
	CHits                  = "hits"
	CBlocks                = "blocks"
	CTimeOnIce             = "time_on_ice"
	CShifts                = "shifts"
	CTakeAways             = "take_aways"
	CGiveAways             = "give_aways"
	CFirstName             = "first_name"
	CLastName              = "last_name"
	CUniformNumber         = "uniform_number"
	CHomeURL               = "home_url"
	CImageSmall            = "image_small"
	CImageMedium           = "image_medium"
	CImageLarge            = "image_large"
	CNHLConferenceID       = "nhl_conference_id"
	CCity                  = "city"
	CAbbreviation          = "abbreviation"
	CNHLDivisionID         = "nhl_division_id"
	CScheduleLink          = "schedule_link"
	CNHLHomeLink           = "nhl_home_link"
	CYahooHomeLink         = "yahoo_home_link"
	CLogoURL               = "logo_url"
	CSmallLogoURL          = "small_logo_url"
	CLargeLogoURL          = "large_logo_url"
	CAllStars              = "all_stars"
	CTeamID                = "team_id"
	CState                 = "state"
	CKey                   = "key"
	CURL                   = "url"
	CDraftStatus           = "draft_status"
	CNumTeams              = "num_teams"
	CEditKey               = "edit_key"
	CLeagueUpdateTimestamp = "league_update_timestamp"
	CScoringType           = "scoring_type"
	CLeagueType            = "league_type"
	CIsProLeague           = "is_pro_league"
	CIsCashLeague          = "is_cash_league"
	CStartDate             = "start_date"
	CEndDate               = "end_date"
	CGameCode              = "game_code"
	CSeason                = "season"
	CDraftType             = "draft_type"
	CIsAuctionDraft        = "is_auction_draft"
	CPersistentURL         = "persistent_url"
	CUsesPlayoff           = "uses_playoff"
	CWaiverType            = "waiver_type"
	CWaiverRule            = "waiver_rule"
	CDraftTime             = "draft_time"
	CDrafTPickTime         = "draft_pick_time"
	CPostDraftPlayers      = "post_draft_players"
	CMaxTeams              = "max_teams"
	CWaiverTime            = "waiver_time"
	CTradeEndDate          = "trade_end_date"
	CTradeRatifyType       = "trade_ratify_type"
	CTradeRejectTime       = "trade_reject_time"
	CPlayerPool            = "player_pool"
	CCantCutList           = "cant_cut_list"
	CSendbirdChannelURL    = "sendbird_channel_url"
	CIsOwnedByCurrentLogin = "is_owned_by_current_login"
	CDraftPosition         = "draft_position"
	CHasDraftGrade         = "has_draft_grade"
	CManagerID             = "manager_id"
	CNickname              = "nickname"
	CGUID                  = "guid"
	CEMail                 = "e_mail"
	CImageUL               = "image_url"
	CEligiblePositions     = "eligible_positions"
	CSelectedPosition      = "selected_position"
)

// Table names
const (
	TblPlayerStats    = "player_stats"
	TblPlayers        = "players"
	TblGames          = "games"
	TblNHLConferences = "nhl_conferences"
	TblNHLDivisions   = "nhl_divisions"
	TblNHLTeams       = "nhl_teams"
	TblTeamSummaries  = "team_summaries"
	TblLeagues        = "leagues"
	TblTeams          = "teams"
	TblRosterPlayers  = "roster_players"
)

// Attribute names
const (
	AID   = "ID"
	ADate = "Date"
	AName = "Name"
)

var colID = clause.Column{Name: CID}

type DifferentAttr interface {
	Name() string
	Previous() any
	Current() any
}

type Different[T int | uint | int64 | string | bool | time.Time] struct {
	name     string
	previous T
	current  T
}

func getaname(obj any, idx int) string {
	return reflect.Indirect(reflect.ValueOf(obj)).Type().Field(idx).Name
}

// TODO create constraint for T
//func NewDifferent[T int | uint | int64 | string | bool | time.Time](name string, previous T, current T) *Different[T] {
//	return &Different[T]{name: name, previous: previous, current: current}
//}

func (d *Different[T]) Name() string {
	return d.name
}

func (d *Different[T]) Previous() any {
	return d.previous
}

func (d *Different[T]) Current() any {
	return d.current
}

type Object interface {
	*League | *Team | *RosterPlayer | *PlayerStats | *Player | *TeamSummary | RosterPlayers
	SetSourceVersion(time.Time)
	Ensure(*gorm.DB) error
}
