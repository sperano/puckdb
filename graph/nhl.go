package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/sqlcdb"
	"gorm.io/gorm"
)

func nhlConferences(ctx context.Context, q *sqlcdb.Queries) ([]*model.NHLConference, error) {
	confs, err := q.GetAllNHLConferences(ctx)
	if err != nil {
		return nil, fmt.Errorf("find all nhl conferences: %w", err)
	}
	gqlConfs := make([]*model.NHLConference, len(confs))
	for i, c := range confs {
		gqlConfs[i] = &model.NHLConference{
			ID:   int(c.ID),
			Name: c.Name,
		}
	}
	return gqlConfs, nil
}

func sqlcDivisionToGQL(row sqlcdb.GetAllNHLDivisionsRow) *model.NHLDivision {
	return &model.NHLDivision{
		ID:   int(row.ID),
		Name: row.Name,
		Conference: &model.NHLConference{
			ID:   int(row.ConfID),
			Name: row.ConfName,
		},
	}
}

func nhlDivisions(ctx context.Context, q *sqlcdb.Queries) ([]*model.NHLDivision, error) {
	divs, err := q.GetAllNHLDivisions(ctx)
	if err != nil {
		return nil, fmt.Errorf("find all nhl divisions: %w", err)
	}
	gqlDivs := make([]*model.NHLDivision, len(divs))
	for i, d := range divs {
		gqlDivs[i] = sqlcDivisionToGQL(d)
	}
	return gqlDivs, nil
}

func sqlcTeamRowToGQL(row sqlcdb.GetAllNHLTeamsRow) *model.NHLTeam {
	return &model.NHLTeam{
		ID:            int(row.ID),
		City:          row.City,
		Name:          row.Name,
		Abbreviation:  row.Abbreviation,
		NHLHomeLink:   row.NHLHomeLink,
		YahooHomeLink: row.YahooHomeLink,
		SmallLogoURL:  row.SmallLogoURL,
		LargeLogoURL:  row.LargeLogoURL,
		Division: &model.NHLDivision{
			ID:   int(row.DivID),
			Name: row.DivName,
			Conference: &model.NHLConference{
				ID:   int(row.ConfID),
				Name: row.ConfName,
			},
		},
	}
}

func sqlcSingleTeamRowToGQL(row sqlcdb.GetNHLTeamRow) *model.NHLTeam {
	return &model.NHLTeam{
		ID:            int(row.ID),
		City:          row.City,
		Name:          row.Name,
		Abbreviation:  row.Abbreviation,
		NHLHomeLink:   row.NHLHomeLink,
		YahooHomeLink: row.YahooHomeLink,
		SmallLogoURL:  row.SmallLogoURL,
		LargeLogoURL:  row.LargeLogoURL,
		Division: &model.NHLDivision{
			ID:   int(row.DivID),
			Name: row.DivName,
			Conference: &model.NHLConference{
				ID:   int(row.ConfID),
				Name: row.ConfName,
			},
		},
	}
}

func nhlTeams(ctx context.Context, q *sqlcdb.Queries, allstars bool) ([]*model.NHLTeam, error) {
	teams, err := q.GetAllNHLTeams(ctx, allstars)
	if err != nil {
		return nil, fmt.Errorf("find all nhl teams: %w", err)
	}
	gqlTeams := make([]*model.NHLTeam, len(teams))
	for i, t := range teams {
		gqlTeams[i] = sqlcTeamRowToGQL(t)
	}
	return gqlTeams, nil
}

func nhlTeam(ctx context.Context, q *sqlcdb.Queries, teamID int) (*model.NHLTeam, error) {
	team, err := q.GetNHLTeam(ctx, int64(teamID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn().Msg("No nhl team found in database")
			return nil, nil
		}
		return nil, fmt.Errorf("find a nhl team: %w", err)
	}
	return sqlcSingleTeamRowToGQL(team), nil
}

type StandingsMap map[uint]*model.NHLTeamStanding

func standingsForTeam(ctx context.Context, q *sqlcdb.Queries, standingsMap StandingsMap, teamID uint) (*model.NHLTeamStanding, error) {
	standings, ok := standingsMap[teamID]
	if !ok {
		standings = &model.NHLTeamStanding{
			Away: &model.NHLStandingsTeamStats{},
			Home: &model.NHLStandingsTeamStats{},
		}
		t, err := nhlTeam(ctx, q, int(teamID))
		if err != nil {
			return nil, err
		}
		standings.NHLTeam = t
		standingsMap[teamID] = standings
	}
	return standings, nil
}

func common1(isAWin bool, game *database.Game, stats *model.NHLStandingsTeamStats) {
	stats.GamesPlayed++
	if isAWin {
		if game.IsOvertime() {
			stats.OvertimeWins++
		} else if game.IsShootout() {
			stats.ShootoutWins++
		} else {
			stats.Wins++
		}
	} else {
		if game.IsOvertime() {
			stats.OvertimeLosses++
		} else if game.IsShootout() {
			stats.ShootoutLosses++
		} else {
			stats.Losses++
		}
	}
}

func getPts(s *model.NHLTeamStanding) int {
	return ((s.Home.Wins + s.Home.OvertimeWins + s.Home.ShootoutWins +
		s.Away.Wins + s.Away.OvertimeWins + s.Away.ShootoutWins) * 2) +
		s.Home.OvertimeLosses + s.Home.ShootoutLosses +
		s.Away.OvertimeLosses + s.Away.OvertimeLosses
}

func NhlStandings(ctx context.Context, db *gorm.DB, q *sqlcdb.Queries) ([]*model.NHLTeamStanding, error) {
	var games []*database.Game
	result := db.Find(&games)
	if result.Error != nil {
		return nil, fmt.Errorf("find all nhl teams: %w", result.Error)
	}
	standingsMap := StandingsMap{}
	for _, game := range games {
		// do standings for away team
		standings, err := standingsForTeam(ctx, q, standingsMap, game.AwayTeamID)
		if err != nil {
			return nil, err
		}
		common1(!game.HomeTeamWins(), game, standings.Away)
		standings.Away.GoalsFor += game.CountingAwayTeamScore()
		standings.Away.GoalsAgainst += game.CountingHomeTeamScore()
		//
		standings, err = standingsForTeam(ctx, q, standingsMap, game.HomeTeamID)
		if err != nil {
			return nil, err
		}
		common1(game.HomeTeamWins(), game, standings.Home)
		standings.Home.GoalsFor += game.CountingHomeTeamScore()
		standings.Home.GoalsAgainst += game.CountingAwayTeamScore()
	}
	allStandings := make([]*model.NHLTeamStanding, len(standingsMap))
	i := 0
	for _, v := range standingsMap {
		allStandings[i] = v
		i++
	}
	sort.Slice(allStandings, func(i, j int) bool {
		pi := getPts(allStandings[i])
		pj := getPts(allStandings[j])
		return pi > pj
	})
	return allStandings, nil
}
