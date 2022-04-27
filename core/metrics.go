package core

import "github.com/penglongli/gin-metrics/ginmetrics"

const MetricTaskPublished = "yfh_tasks_published"

func CreateMetricTaskPublished(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Counter,
		Name:        MetricTaskPublished,
		Description: "The number of task published",
		Labels:      []string{"task_type"},
	})
}

const MetricTaskConsumed = "yfh_tasks_consumed"

func CreateMetricTaskConsumed(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Counter,
		Name:        MetricTaskConsumed,
		Description: "The number of task consumed",
		Labels:      []string{"task_type"},
	})
}

const MetricDownloaded = "yfh_downloads"

func CreateMetricDownload(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Counter,
		Name:        MetricDownloaded,
		Description: "The number of downloads",
		Labels:      []string{"url"},
	})
}

const MetricElapseTime = "yfh_worker_elapse_time"

func CreateMetricElapseTime(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Counter,
		Name:        MetricElapseTime,
		Description: "Total time spent executing workers",
	})
}

const MetricNHLConferences = "yfh_nhl_conferences"

func CreateMetricNHLConferences(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricNHLConferences,
		Description: "The number of nhl conferences",
	})
}

const MetricNHLDivisions = "yfh_nhl_divisions"

func CreateMetricNHLDivisions(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricNHLDivisions,
		Description: "The number of nhl divisions",
	})
}

const MetricNHLTeams = "yfh_nhl_teams"

func CreateMetricNHLTeams(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricNHLTeams,
		Description: "The number of nhl teams",
	})
}

const MetricFantasyGames = "yfh_fantasy_games"

func CreateMetricFantasyGames(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricFantasyGames,
		Description: "The number of fantasy games",
	})
}

const MetricLeagues = "yfh_leagues"

func CreateMetricLeagues(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricLeagues,
		Description: "The number of leagues",
	})
}

const MetricTeams = "yfh_teams"

func CreateMetricTeams(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricTeams,
		Description: "The number of teams",
	})
}

const MetricPlayers = "yfh_players"

func CreateMetricPlayers(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricPlayers,
		Description: "The number of players",
	})
}

const MetricGames = "yfh_games"

func CreateMetricGames(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricGames,
		Description: "The number of games",
	})
}

const MetricPlayerStats = "yfh_player_stats"

func CreateMetricPlayerStats(monitor *ginmetrics.Monitor) {
	monitor.AddMetric(&ginmetrics.Metric{
		Type:        ginmetrics.Gauge,
		Name:        MetricPlayerStats,
		Description: "The number of player stats",
	})
}
