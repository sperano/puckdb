package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/sqlcdb"
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
