package mcpserver

import (
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// Units of the Edge measurements stored in both systems.
const (
	unitMPH   = "mph"
	unitKMH   = "km/h"
	unitMiles = "miles"
	unitKM    = "km"
)

// edgeMetricInfo describes one get_edge_leaders metric. Its key is passed
// to the leaders queries, whose CASE maps it to a column; the key lists
// here and there must match (TestEdgeLeadersEveryMetricSortsInSQL).
type edgeMetricInfo struct {
	key string
	// ascending ranks the lowest value first.
	ascending bool
	// unit is the value column's unit and metricUnit the value_metric
	// column's; empty for counts, ratios and shares.
	unit, metricUnit string
}

// edgeMetricValues are one row's output cells for a metric; an empty cell
// is a NULL or a figure the metric does not have.
type edgeMetricValues struct {
	value, valueMetric, standing, leagueAvg, leagueAvgMetric string
}

// edgeMetric is a metric and how to read its cells from a query row T.
type edgeMetric[T any] struct {
	edgeMetricInfo
	values func(T) edgeMetricValues
}

// edgeMetricSet is a group's whitelist, in the order descriptions list it.
type edgeMetricSet[T any] []edgeMetric[T]

func (s edgeMetricSet[T]) infos() []edgeMetricInfo {
	infos := make([]edgeMetricInfo, len(s))
	for i, m := range s {
		infos[i] = m.edgeMetricInfo
	}
	return infos
}

// get returns the metric for key, which the caller took from infos().
func (s edgeMetricSet[T]) get(key string) edgeMetric[T] {
	for _, m := range s {
		if m.key == key {
			return m
		}
	}
	panic("edge metric " + key + " is not whitelisted")
}

type (
	skaterLeader = sqlcdb.GetEdgeSkaterLeadersRow
	goalieLeader = sqlcdb.GetEdgeGoalieLeadersRow
	teamLeader   = sqlcdb.GetEdgeTeamLeadersRow
)

var skaterEdgeMetrics = edgeMetricSet[skaterLeader]{
	{edgeMetricInfo{key: "top_speed", unit: unitMPH, metricUnit: unitKMH}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{f4(r.TopSpeedImperial), f4(r.TopSpeedMetric), f4(r.TopSpeedPercentile), f4(r.TopSpeedLeagueAvgImperial), f4(r.TopSpeedLeagueAvgMetric)}
	}},
	{edgeMetricInfo{key: "bursts_over_20"}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{value: i4(r.BurstsOver20), standing: f4(r.BurstsOver20Percentile), leagueAvg: f4(r.BurstsOver20LeagueAvg)}
	}},
	{edgeMetricInfo{key: "total_distance", unit: unitMiles, metricUnit: unitKM}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.TotalDistanceImperial), valueMetric: f4(r.TotalDistanceMetric), standing: f4(r.TotalDistancePercentile)}
	}},
	{edgeMetricInfo{key: "max_game_distance", unit: unitMiles, metricUnit: unitKM}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.MaxGameDistanceImperial), valueMetric: f4(r.MaxGameDistanceMetric), standing: f4(r.MaxGameDistancePercentile)}
	}},
	{edgeMetricInfo{key: "top_shot_speed", unit: unitMPH, metricUnit: unitKMH}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{f4(r.TopShotSpeedImperial), f4(r.TopShotSpeedMetric), f4(r.TopShotSpeedPercentile), f4(r.TopShotSpeedLeagueAvgImperial), f4(r.TopShotSpeedLeagueAvgMetric)}
	}},
	{edgeMetricInfo{key: "oz_pctg"}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.OzPctg), standing: f4(r.OzPercentile), leagueAvg: f4(r.OzLeagueAvg)}
	}},
	{edgeMetricInfo{key: "oz_ev_pctg"}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.OzEvPctg), standing: f4(r.OzEvPercentile)}
	}},
	{edgeMetricInfo{key: "nz_pctg"}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.NzPctg), standing: f4(r.NzPercentile), leagueAvg: f4(r.NzLeagueAvg)}
	}},
	{edgeMetricInfo{key: "dz_pctg", ascending: true}, func(r skaterLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.DzPctg), standing: f4(r.DzPercentile), leagueAvg: f4(r.DzLeagueAvg)}
	}},
}

var goalieEdgeMetrics = edgeMetricSet[goalieLeader]{
	{edgeMetricInfo{key: "gaa", ascending: true}, func(r goalieLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.GaaValue), standing: f4(r.GaaPercentile), leagueAvg: f4(r.GaaLeagueAvg)}
	}},
	{edgeMetricInfo{key: "games_above_900"}, func(r goalieLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.GamesAbove900Value), standing: f4(r.GamesAbove900Percentile), leagueAvg: f4(r.GamesAbove900LeagueAvg)}
	}},
	{edgeMetricInfo{key: "goal_diff_per_60"}, func(r goalieLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.GoalDiffPer60Value), standing: f4(r.GoalDiffPer60Percentile), leagueAvg: f4(r.GoalDiffPer60LeagueAvg)}
	}},
	{edgeMetricInfo{key: "goal_support_avg"}, func(r goalieLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.GoalSupportAvgValue), standing: f4(r.GoalSupportAvgPercentile), leagueAvg: f4(r.GoalSupportAvgLeagueAvg)}
	}},
	{edgeMetricInfo{key: "point_pctg"}, func(r goalieLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.PointPctgValue), standing: f4(r.PointPctgPercentile), leagueAvg: f4(r.PointPctgLeagueAvg)}
	}},
}

var teamEdgeMetrics = edgeMetricSet[teamLeader]{
	{edgeMetricInfo{key: "shot_attempts_over_90"}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: i4(r.ShotAttemptsOver90), standing: i4(r.ShotAttemptsOver90Rank)}
	}},
	{edgeMetricInfo{key: "top_shot_speed", unit: unitMPH, metricUnit: unitKMH}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.TopShotSpeedImperial), valueMetric: f4(r.TopShotSpeedMetric), standing: i4(r.TopShotSpeedRank)}
	}},
	{edgeMetricInfo{key: "bursts_over_22"}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: i4(r.BurstsOver22), standing: i4(r.BurstsOver22Rank)}
	}},
	{edgeMetricInfo{key: "bursts_over_20"}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: i4(r.BurstsOver20), standing: i4(r.BurstsOver20Rank)}
	}},
	{edgeMetricInfo{key: "top_speed", unit: unitMPH, metricUnit: unitKMH}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.SpeedMaxImperial), valueMetric: f4(r.SpeedMaxMetric), standing: i4(r.SpeedMaxRank)}
	}},
	{edgeMetricInfo{key: "total_distance"}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: i4(r.TotalDistance), standing: i4(r.TotalDistanceRank)}
	}},
	{edgeMetricInfo{key: "oz_pctg"}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.OzPctg), standing: i4(r.OzRank), leagueAvg: f4(r.OzLeagueAvg)}
	}},
	{edgeMetricInfo{key: "oz_ev_pctg"}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.OzEvPctg), standing: i4(r.OzEvRank)}
	}},
	{edgeMetricInfo{key: "nz_pctg"}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.NzPctg), standing: i4(r.NzRank), leagueAvg: f4(r.NzLeagueAvg)}
	}},
	{edgeMetricInfo{key: "dz_pctg", ascending: true}, func(r teamLeader) edgeMetricValues {
		return edgeMetricValues{value: f4(r.DzPctg), standing: i4(r.DzRank), leagueAvg: f4(r.DzLeagueAvg)}
	}},
}

// edgeMetricInfos is the whitelist per group.
var edgeMetricInfos = map[string][]edgeMetricInfo{
	edgeGroupSkater: skaterEdgeMetrics.infos(),
	edgeGroupGoalie: goalieEdgeMetrics.infos(),
	edgeGroupTeam:   teamEdgeMetrics.infos(),
}

func metricInfoKeys(metrics []edgeMetricInfo) string {
	keys := make([]string, len(metrics))
	for i, m := range metrics {
		keys[i] = m.key
	}
	return strings.Join(keys, ", ")
}

// edgePlayerLeaderRow is one skater or goalie of get_edge_leaders; the
// header names the metric and units of value and value_metric.
type edgePlayerLeaderRow struct {
	Rank            int         `json:"rank"`
	PlayerID        int64       `json:"player_id"`
	FirstName       string      `json:"first_name"`
	LastName        string      `json:"last_name"`
	Position        string      `json:"position"`
	Teams           string      `json:"teams"`
	GamesPlayed     pgtype.Int4 `json:"gp"`
	Value           string      `json:"value"`
	ValueMetric     string      `json:"value_metric"`
	Percentile      string      `json:"percentile"`
	LeagueAvg       string      `json:"league_avg"`
	LeagueAvgMetric string      `json:"league_avg_metric"`
}

// edgeTeamLeaderRow is one team of get_edge_leaders; league_rank is the
// NHL's own rank for the metric.
type edgeTeamLeaderRow struct {
	Rank        int    `json:"rank"`
	TeamID      int64  `json:"team_id"`
	Abbrev      string `json:"abbrev"`
	FullName    string `json:"full_name"`
	Value       string `json:"value"`
	ValueMetric string `json:"value_metric"`
	LeagueRank  string `json:"league_rank"`
	LeagueAvg   string `json:"league_avg"`
}

// edgeLeaderPlayer is the identity part of a skater or goalie leader row.
type edgeLeaderPlayer struct {
	playerID            int64
	firstName, lastName string
	position            sqlcdb.NullPlayerPosition
	gamesPlayed         int32
	teams               string
}

func skaterLeaderPlayer(r skaterLeader) edgeLeaderPlayer {
	return edgeLeaderPlayer{r.PlayerID, r.FirstName, r.LastName, r.Position, r.GamesPlayed, r.Teams}
}

func goalieLeaderPlayer(r goalieLeader) edgeLeaderPlayer {
	return edgeLeaderPlayer{r.PlayerID, r.FirstName, r.LastName, r.Position, r.GamesPlayed, r.Teams}
}

// playerLeaderRows converts query rows, already in leader order, to output
// rows. A player has at least one game when Edge tracked him, so 0 games
// means no club stats were imported and prints empty.
func playerLeaderRows[T any](rows []T, metric edgeMetric[T], player func(T) edgeLeaderPlayer) []edgePlayerLeaderRow {
	out := make([]edgePlayerLeaderRow, len(rows))
	for i, row := range rows {
		p, v := player(row), metric.values(row)
		out[i] = edgePlayerLeaderRow{
			PlayerID: p.playerID, FirstName: p.firstName, LastName: p.lastName, Teams: p.teams,
			GamesPlayed: pgtype.Int4{Int32: p.gamesPlayed, Valid: p.gamesPlayed > 0},
			Value:       v.value, ValueMetric: v.valueMetric, Percentile: v.standing,
			LeagueAvg: v.leagueAvg, LeagueAvgMetric: v.leagueAvgMetric,
		}
		if p.position.Valid {
			out[i].Position = string(p.position.PlayerPosition)
		}
	}
	setCompetitionRanks(out, func(r edgePlayerLeaderRow) string { return r.Value }, func(r *edgePlayerLeaderRow, rank int) { r.Rank = rank })
	return out
}

func teamLeaderRows(rows []teamLeader, metric edgeMetric[teamLeader]) []edgeTeamLeaderRow {
	out := make([]edgeTeamLeaderRow, len(rows))
	for i, row := range rows {
		v := metric.values(row)
		out[i] = edgeTeamLeaderRow{
			TeamID: row.TeamID, Abbrev: row.Abbrev, FullName: row.FullName,
			Value: v.value, ValueMetric: v.valueMetric, LeagueRank: v.standing, LeagueAvg: v.leagueAvg,
		}
	}
	setCompetitionRanks(out, func(r edgeTeamLeaderRow) string { return r.Value }, func(r *edgeTeamLeaderRow, rank int) { r.Rank = rank })
	return out
}

// setCompetitionRanks numbers rows already in leader order 1, 2, 2, 4...:
// rows with the same value share the better rank.
func setCompetitionRanks[R any](rows []R, value func(R) string, set func(*R, int)) {
	rank := 0
	for i := range rows {
		if i == 0 || value(rows[i]) != value(rows[i-1]) {
			rank = i + 1
		}
		set(&rows[i], rank)
	}
}

// f4 prints a float4 cell at float32 precision; NULL is empty.
func f4(v pgtype.Float4) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatFloat(float64(v.Float32), 'g', -1, 32)
}

// i4 prints an int4 cell; NULL is empty.
func i4(v pgtype.Int4) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatInt(int64(v.Int32), 10)
}
