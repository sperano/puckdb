// Package yahoofixtures holds Yahoo Fantasy API XML fixtures shared by the
// store, draft and worker tests. Every fixture is synthetic: the 2026 target
// leagues cannot be captured while Yahoo answers 403, so the documents follow
// the Yahoo response shapes with invented leagues and players. Only tests
// import this package.
package yahoofixtures

import (
	"embed"
	"fmt"
)

// Fixture file names.
const (
	// RotoLeague is a 2025 rotisserie league (game 453, league 1003) with the
	// nine Crapettes 2025 categories plus display-only SA and SV.
	RotoLeague = "league-roto.xml"
	// RotoLeagueChanged is RotoLeague after the commissioner swapped PIM for
	// HIT and added a bench spot.
	RotoLeagueChanged = "league-roto-changed.xml"
	// PointsLeague is a 2026 head-to-head points league (game 465, league
	// 77777) with stat modifiers, a bonus and an F flex slot.
	PointsLeague = "league-points.xml"
	// PointsLeagueMissingWeight is PointsLeague without the weight of one
	// scoring stat.
	PointsLeagueMissingWeight = "league-points-missing-weight.xml"
	// PlayersPage is one page of the 2026 points league's player pool.
	PlayersPage = "league-players.xml"
	// H2HStandings is the 2026 points league's standings six weeks in: four
	// ranked teams with outcome totals, streaks and points; one team also
	// carries divisional outcome totals.
	H2HStandings = "league-standings-h2h.xml"
	// PreseasonStandings is the 2026 points league's standings before its
	// first game: two teams with empty ranks, seeds and percentages.
	PreseasonStandings = "league-standings-preseason.xml"
	// RotoStandings is the 2025 rotisserie league's standings: two ranked
	// teams with roto points and no outcome totals.
	RotoStandings = "league-standings-roto.xml"
)

// Identities used by the fixtures.
const (
	RotoSeason     = 2025
	RotoGameKey    = 453
	RotoLeagueID   = 1003
	PointsSeason   = 2026
	PointsGameKey  = 465
	PointsLeagueID = 77777
	// PlayersInPage is how many players PlayersPage lists.
	PlayersInPage = 7
	// TeamsInH2HStandings is how many teams H2HStandings ranks.
	TeamsInH2HStandings = 4
)

//go:embed *.xml
var files embed.FS

// Read returns a fixture's bytes; it panics on an unknown name, which is a
// bug in the calling test.
func Read(name string) []byte {
	data, err := files.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("yahoofixtures: %v", err))
	}
	return data
}
