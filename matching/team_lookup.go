package matching

import "fmt"

// NHLTeamInfo holds static NHL team data for lookup.
type NHLTeamInfo struct {
	ID       int64
	Abbrev   string
	FullName string
}

// teamsByAbbrev maps team abbreviation to team info.
// Generated from NHL API boxscore data extraction.
// Includes all NHL teams (current and historical) plus special event teams.
var teamsByAbbrev = map[string]NHLTeamInfo{
	// Current NHL teams (32 teams)
	"ANA": {24, "ANA", "Anaheim Ducks"},
	"BOS": {6, "BOS", "Boston Bruins"},
	"BUF": {7, "BUF", "Buffalo Sabres"},
	"CAR": {12, "CAR", "Carolina Hurricanes"},
	"CBJ": {29, "CBJ", "Columbus Blue Jackets"},
	"CGY": {20, "CGY", "Calgary Flames"},
	"CHI": {16, "CHI", "Chicago Blackhawks"},
	"COL": {21, "COL", "Colorado Avalanche"},
	"DAL": {25, "DAL", "Dallas Stars"},
	"DET": {17, "DET", "Detroit Red Wings"},
	"EDM": {22, "EDM", "Edmonton Oilers"},
	"FLA": {13, "FLA", "Florida Panthers"},
	"LAK": {26, "LAK", "Los Angeles Kings"},
	"MIN": {30, "MIN", "Minnesota Wild"},
	"MTL": {8, "MTL", "Montréal Canadiens"},
	"NJD": {1, "NJD", "New Jersey Devils"},
	"NSH": {18, "NSH", "Nashville Predators"},
	"NYI": {2, "NYI", "New York Islanders"},
	"NYR": {3, "NYR", "New York Rangers"},
	"OTT": {9, "OTT", "Ottawa Senators"},
	"PHI": {4, "PHI", "Philadelphia Flyers"},
	"PIT": {5, "PIT", "Pittsburgh Penguins"},
	"SEA": {55, "SEA", "Seattle Kraken"},
	"SJS": {28, "SJS", "San Jose Sharks"},
	"STL": {19, "STL", "St. Louis Blues"},
	"TBL": {14, "TBL", "Tampa Bay Lightning"},
	"TOR": {10, "TOR", "Toronto Maple Leafs"},
	"UTA": {59, "UTA", "Utah Hockey Club"},
	"VAN": {23, "VAN", "Vancouver Canucks"},
	"VGK": {54, "VGK", "Vegas Golden Knights"},
	"WPG": {52, "WPG", "Winnipeg Jets"},
	"WSH": {15, "WSH", "Washington Capitals"},

	// Relocated/Renamed teams (historical, still active franchises)
	"ARI": {53, "ARI", "Arizona Coyotes"},
	"ATL": {11, "ATL", "Atlanta Thrashers"},
	"PHX": {27, "PHX", "Phoenix Coyotes"},

	// Defunct NHL teams
	"AFM": {47, "AFM", "Atlanta Flames"},
	"BRK": {51, "BRK", "Brooklyn Americans"},
	"CGS": {56, "CGS", "California Golden Seals"},
	"CBN": {49, "CLE", "Cleveland Barons"}, // Alias used in standings API
	"CLE": {49, "CLE", "Cleveland Barons"},
	"CLR": {35, "CLR", "Colorado Rockies"},
	"CSE": {56, "CGS", "California Golden Seals"}, // Alias
	"DCG": {40, "DCG", "Detroit Cougars"},
	"DFL": {50, "DFL", "Detroit Falcons"},
	"HAM": {37, "HAM", "Hamilton Tigers"},
	"HFD": {34, "HFD", "Hartford Whalers"},
	"KCS": {48, "KCS", "Kansas City Scouts"},
	"MMR": {43, "MMR", "Montreal Maroons"},
	"MNS": {31, "MNS", "Minnesota North Stars"},
	"MWN": {41, "MWN", "Montreal Wanderers"},
	"NYA": {44, "NYA", "New York Americans"},
	"OAK": {46, "OAK", "Oakland Seals"},
	"PIR": {38, "PIR", "Pittsburgh Pirates"},
	"QBD": {42, "QBD", "Quebec Bulldogs"},
	"QUA": {39, "QUA", "Philadelphia Quakers"},
	"QUE": {32, "QUE", "Quebec Nordiques"},
	"SEN": {36, "SEN", "Ottawa Senators (1917)"},
	"SLE": {45, "SLE", "St. Louis Eagles"},
	"TAN": {57, "TAN", "Toronto Arenas"},
	"TSP": {58, "TSP", "Toronto St. Patricks"},
	"WIN": {33, "WIN", "Winnipeg Jets (1979)"},

	// Pre-NHL western league teams (PCHA/WCHL Stanley Cup challengers, 1917-1925)
	"VMI": {70, "VMI", "Vancouver Millionaires"},
	"SMT": {71, "SMT", "Seattle Metropolitans"},
	"EDK": {72, "EDK", "Edmonton Eskimos"},
	"VMR": {73, "VMR", "Vancouver Maroons"},
	"VIC": {74, "VIC", "Victoria Cougars"},

	// International teams (World Cup, Olympics)
	"CAN": {60, "CAN", "Canada"},
	"CZE": {61, "CZE", "Czechia"},
	"FIN": {62, "FIN", "Finland"},
	"GER": {63, "GER", "Germany"},
	"RUS": {64, "RUS", "Russia"},
	"SVK": {65, "SVK", "Slovakia"},
	"SWE": {66, "SWE", "Sweden"},
	"USA": {67, "USA", "USA"},

	// All-Star division teams
	"ATL_AS": {87, "ATL", "Atlantic"},
	"MET":    {88, "MET", "Metropolitan"},
	"CEN":    {89, "CEN", "Central"},
	"PAC":    {90, "PAC", "Pacific"},

	// All-Star captain teams (dynamic names vary by year)
	"RED": {91, "RED", "All-Star Team Red"},
	"BLU": {92, "BLU", "All-Star Team Blue"},
	"BLK": {93, "BLK", "All-Star Team Black"},
	"WHT": {94, "WHT", "All-Star Team White"},
	"ASR": {95, "ASR", "All-Star Team Red"},
	"ASB": {96, "ASB", "All-Star Team Blue"},
	"ASE": {97, "ASE", "All-Stars East"},
	"ASW": {98, "ASW", "All-Stars West"},

	// Young Stars teams
	"YSE": {100, "YSE", "Young Stars East"},
	"YSW": {101, "YSW", "Young Stars West"},
}

// teamsByID provides reverse lookup by team ID.
var teamsByID map[int64]NHLTeamInfo

// teamsByName provides reverse lookup by full team name.
var teamsByName map[string]NHLTeamInfo

func init() {
	teamsByID = make(map[int64]NHLTeamInfo, len(teamsByAbbrev))
	teamsByName = make(map[string]NHLTeamInfo, len(teamsByAbbrev))
	for _, t := range teamsByAbbrev {
		// Only store the first occurrence (some abbrevs map to same ID like CGS/CSE)
		if _, exists := teamsByID[t.ID]; !exists {
			teamsByID[t.ID] = t
		}
		teamsByName[t.FullName] = t
	}
}

// LookupTeamID returns the team ID for an abbreviation.
// Returns an error if the abbreviation is unknown.
func LookupTeamID(abbrev string) (int64, error) {
	if info, ok := teamsByAbbrev[abbrev]; ok {
		return info.ID, nil
	}
	return 0, fmt.Errorf("unknown team abbreviation: %q", abbrev)
}

// LookupTeamByID returns team info by ID.
// Returns an error if the ID is unknown.
func LookupTeamByID(id int64) (NHLTeamInfo, error) {
	if info, ok := teamsByID[id]; ok {
		return info, nil
	}
	return NHLTeamInfo{}, fmt.Errorf("unknown team ID: %d", id)
}

// LookupTeamIDByName returns the team ID for a full team name.
// Returns an error if the name is unknown.
func LookupTeamIDByName(fullName string) (int64, error) {
	if info, ok := teamsByName[fullName]; ok {
		return info.ID, nil
	}
	return 0, fmt.Errorf("unknown team name: %q", fullName)
}

// LookupTeamAbbrev returns the abbreviation for a team ID.
// Returns an error if the ID is unknown.
func LookupTeamAbbrev(id int64) (string, error) {
	if info, ok := teamsByID[id]; ok {
		return info.Abbrev, nil
	}
	return "", fmt.Errorf("unknown team ID: %d", id)
}
