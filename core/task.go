package core

import (
	"fmt"
	"sort"
	"strings"
)

const TaskImportFantasyGame = "import_fantasy_game"
const TaskImportLeague = "import_league"
const TaskImportGames = "import_games"
const TaskImportGame = "import_game"
const TaskImportRosters = "import_rosters"
const TaskImportRoster = "import_roster"
const TaskImportTeams = "import_teams"
const TaskImportTeam = "import_team"
const TaskImportAll = "import_all"
const TaskInitMeta = "init_meta"

const TaskDataUser = "user"
const TaskDataTeamID = "team_id"
const TaskDataDate = "date"
const TaskDataGameLink = "game_link"

const TaskComputeAll = "compute_all"
const TaskComputeForDate = "compute_for_date"
const TaskComputeForDateAndTeam = "compute_for_date_and_team"

type Task struct {
	Type string
	Data map[string]string
}

func (t *Task) String() string {
	data := []string{}
	keys := []string{}
	for k := range t.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		data = append(data, fmt.Sprintf("%s: \"%s\"", k, t.Data[k]))
	}
	return fmt.Sprintf("%s {%s}", t.Type, strings.Join(data, ", "))
}
