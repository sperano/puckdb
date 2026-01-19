package graph

import (
	"context"

	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/graph/model"
)

func Games() ([]*model.Game, error) {
	ctx := context.Background()

	var games []*database.Game
	db, err := database.OpenGorm()
	if err != nil {
		return nil, err
	}
	result := db.Preload("HomeTeam").Preload("AwayTeam").Find(&games)
	if result.Error != nil {
		return nil, result.Error
	}

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	q := database.NewQueries(pool)

	gqlGames := make([]*model.Game, len(games))
	for i, game := range games {
		gqlGames[i] = game.GraphQLModel()
		t, err := nhlTeam(ctx, q, int(game.HomeTeamID))
		if err != nil {
			return nil, err
		}
		gqlGames[i].HomeTeam = t
		t, err = nhlTeam(ctx, q, int(game.AwayTeamID))
		if err != nil {
			return nil, err
		}
		gqlGames[i].AwayTeam = t
	}
	return gqlGames, nil
}
