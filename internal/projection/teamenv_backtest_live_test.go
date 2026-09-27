//go:build livenhl

package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The team-environment backtest reads regular-season aggregates from the
// public NHL stats API instead of a production database, so anyone can
// rerun it:
//
//	go test -tags=livenhl -run TestTeamEnvironmentBacktest -v ./internal/projection/
//
// Movers are skaters with exactly one club in the season before the target
// season and exactly one, different, club in the target season (a
// multi-club season has no single club to compare, so those players are
// left out rather than guessed). History rows split across clubs count as a
// league-average environment (TeamID 0). The API's power-play
// opportunities are the official count; production derives them from
// penalty events (see ListProjectionTeamSeasons).

const (
	statsAPIBase         = "https://api.nhle.com/stats/rest/en/"
	statsAPITimeout      = 60 * time.Second
	backtestHistoryYears = DefaultLookbackSeasons
)

var backtestTargets = []int{20242025, 20252026}

type statsSkaterRow struct {
	PlayerID         int64   `json:"playerId"`
	SeasonID         int     `json:"seasonId"`
	TeamAbbrevs      string  `json:"teamAbbrevs"`
	PositionCode     string  `json:"positionCode"`
	GamesPlayed      int     `json:"gamesPlayed"`
	TimeOnIcePerGame float64 `json:"timeOnIcePerGame"`
	Goals            int     `json:"goals"`
	Assists          int     `json:"assists"`
	PlusMinus        int     `json:"plusMinus"`
	PenaltyMinutes   int     `json:"penaltyMinutes"`
	PPPoints         int     `json:"ppPoints"`
	Shots            int     `json:"shots"`
}

type statsTeamSummaryRow struct {
	TeamID          int64   `json:"teamId"`
	SeasonID        int     `json:"seasonId"`
	GamesPlayed     int     `json:"gamesPlayed"`
	GoalsFor        int     `json:"goalsFor"`
	ShotsForPerGame float64 `json:"shotsForPerGame"`
}

type statsPowerPlayRow struct {
	TeamID          int64 `json:"teamId"`
	SeasonID        int   `json:"seasonId"`
	PPOpportunities int   `json:"ppOpportunities"`
}

type statsTeamRow struct {
	ID      int64  `json:"id"`
	TriCode string `json:"triCode"`
}

func fetchStats[T any](t *testing.T, report string, season int) []T {
	t.Helper()
	query := url.Values{}
	query.Set("isAggregate", "false")
	query.Set("isGame", "false")
	query.Set("start", "0")
	query.Set("limit", "-1")
	if season > 0 {
		query.Set("cayenneExp", fmt.Sprintf("seasonId=%d and gameTypeId=2", season))
	}
	ctx, cancel := context.WithTimeout(context.Background(), statsAPITimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, statsAPIBase+report+"?"+query.Encode(), nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode, report)
	var body struct {
		Data []T `json:"data"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	return body.Data
}

type backtestData struct {
	skaters []SkaterSeason
	teams   []TeamSeason
	// clubs maps a player's season to their single club, 0 for several.
	clubs map[int]map[int64]int64
}

func loadBacktestData(t *testing.T, seasons []int) backtestData {
	t.Helper()
	names := make(map[int64]string)
	for _, team := range fetchStats[statsTeamRow](t, "team", 0) {
		names[team.ID] = team.TriCode
	}
	data := backtestData{clubs: make(map[int]map[int64]int64)}
	for _, season := range seasons {
		byAbbrev := loadBacktestTeams(t, season, names, &data)
		data.clubs[season] = make(map[int64]int64)
		for _, row := range fetchStats[statsSkaterRow](t, "skater/summary", season) {
			teamID := byAbbrev[row.TeamAbbrevs] // 0 when the row spans clubs
			data.clubs[season][row.PlayerID] = teamID
			data.skaters = append(data.skaters, SkaterSeason{
				PlayerID: row.PlayerID, TeamID: teamID, Season: season, Position: backtestPosition(row.PositionCode),
				GamesPlayed: row.GamesPlayed, TOISeconds: int(math.Round(row.TimeOnIcePerGame * float64(row.GamesPlayed))),
				Goals: row.Goals, Assists: row.Assists, PlusMinus: row.PlusMinus, PenaltyMinutes: row.PenaltyMinutes,
				PowerPlayPoints: row.PPPoints, ShotsOnGoal: row.Shots,
			})
		}
	}
	return data
}

func loadBacktestTeams(t *testing.T, season int, names map[int64]string, data *backtestData) map[string]int64 {
	t.Helper()
	opportunities := make(map[int64]int)
	for _, row := range fetchStats[statsPowerPlayRow](t, "team/powerplay", season) {
		opportunities[row.TeamID] = row.PPOpportunities
	}
	byAbbrev := make(map[string]int64)
	for _, row := range fetchStats[statsTeamSummaryRow](t, "team/summary", season) {
		byAbbrev[names[row.TeamID]] = row.TeamID
		data.teams = append(data.teams, TeamSeason{
			TeamID: row.TeamID, Abbrev: names[row.TeamID], Season: season, GamesPlayed: row.GamesPlayed,
			GoalsFor: row.GoalsFor, ShotsFor: int(math.Round(row.ShotsForPerGame * float64(row.GamesPlayed))),
			PowerPlayGames: row.GamesPlayed, PowerPlayOpportunities: opportunities[row.TeamID],
		})
	}
	return byAbbrev
}

// backtestPosition maps the stats API's L/R wing codes to the model's.
func backtestPosition(code string) string {
	switch code {
	case "L":
		return "LW"
	case "R":
		return "RW"
	default:
		return code
	}
}

// backtestInput projects target from the seasons before it, with target
// clubs for the movers only.
func backtestInput(data backtestData, target int) Input {
	previous := historyFloorSeason(target, 1)
	var targets []PlayerTeam
	for playerID, club := range data.clubs[target] {
		before, played := data.clubs[previous][playerID]
		if club > 0 && played && before > 0 && before != club {
			targets = append(targets, PlayerTeam{PlayerID: playerID, TeamID: club})
		}
	}
	start := time.Date(seasonStartYear(target), time.September, 1, 0, 0, 0, 0, time.UTC)
	return Input{
		TargetSeason: target, AsOf: start, ObservedAt: start.AddDate(1, 0, 0),
		Skaters: data.skaters, TeamSeasons: data.teams, TargetTeams: targets,
	}
}

func TestTeamEnvironmentBacktest(t *testing.T) {
	first := historyFloorSeason(backtestTargets[0], backtestHistoryYears)
	var seasons []int
	for season := first; season <= backtestTargets[len(backtestTargets)-1]; season += 10_001 {
		seasons = append(seasons, season)
	}
	data := loadBacktestData(t, seasons)

	for _, target := range backtestTargets {
		input := backtestInput(data, target)
		evaluations, err := EvaluateTeamChanges(DefaultConfig(), input)
		require.NoError(t, err)
		logBacktest(t, target, evaluations)
		logBacktestRates(t, input)
	}
	logBacktestGrid(t, data)
}

// logBacktestRates compares the movers' per-60 rates, weighted by their
// actual TOI, which isolates the rate the adjustment scales from the
// games-played error that dominates season totals.
func logBacktestRates(t *testing.T, input Input) {
	t.Helper()
	cfg := DefaultConfig()
	adjusted, err := Generate(cfg, input)
	require.NoError(t, err)
	cfg.TeamEnvironmentMaxChange = 0
	unadjusted, err := Generate(cfg, input)
	require.NoError(t, err)
	unadjustedValues := snapshotMeans(unadjusted)
	actual := aggregateSkaterValues(input.Skaters, input.TargetSeason)
	var lines []string
	for _, stat := range []Stat{StatGoals, StatAssists, StatShotsOnGoal, StatPowerPlayPoints} {
		var adjustedError, unadjustedError float64
		var players int
		for _, player := range adjusted.Players {
			outcome, observed := actual[player.PlayerKey]
			projectedTOI := player.Values[StatTOISeconds].Mean
			env := player.TeamEnvironment
			if env == nil || env.FromTeamID == env.ToTeamID || !observed || outcome[StatTOISeconds] <= 0 || projectedTOI <= 0 {
				continue
			}
			rate := outcome[stat] / outcome[StatTOISeconds] * secondsPerHour
			weight := outcome[StatTOISeconds]
			adjustedError += weight * math.Abs(player.Values[stat].Mean/projectedTOI*secondsPerHour-rate)
			unadjustedError += weight * math.Abs(unadjustedValues[player.PlayerKey][stat]/projectedTOI*secondsPerHour-rate)
			players++
		}
		lines = append(lines, fmt.Sprintf("| %s | %d | %.4f |", stat, players, adjustedError/unadjustedError))
	}
	t.Logf("target %d per-60 rate MAE, adjusted / unadjusted\n%s", input.TargetSeason, strings.Join(lines, "\n"))
}

func logBacktest(t *testing.T, target int, evaluations []Evaluation) {
	t.Helper()
	byStat := make(map[Stat][]Metric)
	for _, evaluation := range evaluations {
		for _, metric := range evaluation.Metrics {
			byStat[metric.Stat] = append(byStat[metric.Stat], metric)
		}
	}
	var lines []string
	for _, stat := range teamDependentStats {
		metrics := byStat[stat]
		if len(metrics) != len(evaluations) {
			continue
		}
		lines = append(lines, fmt.Sprintf("| %s | %d | %.2f | %.2f | %.2f |", stat, metrics[0].SampleSize,
			metrics[0].ModelMAE, metrics[0].ComparisonMAE, metrics[1].ComparisonMAE))
	}
	t.Logf("target %d (model MAE | %s MAE | %s MAE)\n%s", target,
		evaluations[0].ComparisonModel, evaluations[1].ComparisonModel, strings.Join(lines, "\n"))
}

// logBacktestGrid reports, per parameter pair, the mean ratio of adjusted
// to unadjusted MAE over the team-dependent stats and target seasons.
func logBacktestGrid(t *testing.T, data backtestData) {
	t.Helper()
	priors := []float64{0, 41, 82, 164, 328}
	limits := []float64{0.05, 0.10, 0.15, 0.25}
	for _, prior := range priors {
		row := make([]string, 0, len(limits))
		for _, limit := range limits {
			cfg := DefaultConfig()
			cfg.TeamEnvironmentPriorGames, cfg.TeamEnvironmentMaxChange = prior, limit
			var ratio float64
			var cells int
			for _, target := range backtestTargets {
				evaluations, err := EvaluateTeamChanges(cfg, backtestInput(data, target))
				require.NoError(t, err)
				for _, metric := range evaluations[0].Metrics {
					ratio += metric.ModelMAE / metric.ComparisonMAE
					cells++
				}
			}
			row = append(row, fmt.Sprintf("max %.2f: %.4f", limit, ratio/float64(cells)))
		}
		t.Logf("prior %3.0f games | %s", prior, strings.Join(row, " | "))
	}
}
