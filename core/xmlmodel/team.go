package xmlmodel

import (
	"encoding/xml"
	"time"
)

type PlayerName struct {
	XMLName    xml.Name `xml:"name"`
	Full       string   `xml:"full"`
	First      string   `xml:"first"`
	Last       string   `xml:"last"`
	ASCIIFirst string   `xml:"ascii_first"`
	ASCIILast  string   `xml:"ascii_last"`
}

type Player struct {
	XMLName   xml.Name `xml:"player"`
	PlayerKey string   `xml:"player_key"`
	PlayerID  int      `xml:"player_id"`
	Name      PlayerName
}

type Players struct {
	XMLName xml.Name `xml:"players"`
	Slice   []Player `xml:"player"`
	Count   int      `xml:"count,attr"`
}

type Roster struct {
	XMLName      xml.Name `xml:"roster"`
	CoverageType string
	Date         time.Time
	IsEditable   bool
	Players      Players
	//		Slice   []TeamLogo `xml:"team_logo"`

}

type TeamLogo struct {
	XMLName xml.Name `xml:"team_logo"`
	Size    string   `xml:"size"`
	URL     string   `xml:"url"`
}

type TeamLogos struct {
	XMLName xml.Name   `xml:"team_logos"`
	Slice   []TeamLogo `xml:"team_logo"`
}

type Team struct {
	XMLName               xml.Name `xml:"team"`
	Key                   string   `xml:"team_key"`
	ID                    int      `xml:"team_id"`
	Name                  string   `xml:"name"`
	IsOwnedByCurrentLogin bool     `xml:"is_owned_by_current_login"`
	URL                   string   `xml:"url"`
	TeamLogos             TeamLogos
	WaiverPriority        int `xml:"waiver_priority"`
	NumberOfMoves         int `xml:"number_of_moves"`
	NumberOfTrades        int `xml:"number_of_trades"`
	/*
	  <roster_adds>
	   <coverage_type>week</coverage_type>
	   <coverage_value>5</coverage_value>
	   <value>0</value>
	  </roster_adds>
	  <league_scoring_type>roto</league_scoring_type>
	  <draft_position>5</draft_position>
	  <has_draft_grade>0</has_draft_grade>
	  <managers>
	   <manager>
	    <manager_id>7</manager_id>
	    <nickname>Owner</nickname>
	    <guid>EXAMPLEGUID0000000000000000</guid>
	    <is_current_login>1</is_current_login>
	    <email>owner@example.com</email>
	    <image_url>https://s.yimg.com/ag/images/9cdf6123-35bb-42bd-ac8d-b5445b89728b_64sq.jpg</image_url>
	    <felo_score>667</felo_score>
	    <felo_tier>silver</felo_tier>
	   </manager>
	  </managers>

	*/
	Roster Roster
}
