package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// loadTeamSeasons reads the clubs' environments over the history window.
func (r *Repository) loadTeamSeasons(ctx context.Context, targetSeason, lowerSeason int, cutoff pgtype.Date) ([]TeamSeason, error) {
	rows, err := r.queries.ListProjectionTeamSeasons(ctx, sqlcdb.ListProjectionTeamSeasonsParams{
		Season: int32(targetSeason), Season_2: int32(lowerSeason), GameDate: cutoff,
	})
	if err != nil {
		return nil, fmt.Errorf("load projection team environments: %w", err)
	}
	out := make([]TeamSeason, 0, len(rows))
	for _, row := range rows {
		out = append(out, TeamSeason{
			TeamID: row.TeamID, Abbrev: row.Abbrev, Season: int(row.Season), GamesPlayed: int(row.GamesPlayed),
			GoalsFor: int(row.GoalsFor), ShotsFor: int(row.ShotsFor),
			PowerPlayGames: int(row.PowerPlayGames), PowerPlayOpportunities: int(row.PowerPlayOpportunities),
		})
	}
	return out, nil
}

// loadSkaterClubSeasons reads the per-club games of the skater seasons
// split between clubs over the history window.
func (r *Repository) loadSkaterClubSeasons(ctx context.Context, targetSeason, lowerSeason int, cutoff pgtype.Date) ([]SkaterClubSeason, error) {
	rows, err := r.queries.ListProjectionSkaterClubGames(ctx, sqlcdb.ListProjectionSkaterClubGamesParams{
		Season: int32(targetSeason), Season_2: int32(lowerSeason), GameDate: cutoff,
	})
	if err != nil {
		return nil, fmt.Errorf("load projection skater club games: %w", err)
	}
	out := make([]SkaterClubSeason, 0, len(rows))
	for _, row := range rows {
		out = append(out, SkaterClubSeason{
			PlayerID: row.PlayerID, Season: int(row.Season), TeamID: row.TeamID, GamesPlayed: int(row.GamesPlayed),
		})
	}
	return out, nil
}

// loadTeamInputs fills what the team-environment adjustment reads when the
// model supports it: club environments and split-season club games over the
// lookback window (the aging curve's longer training window does not apply
// to clubs), and the target clubs from loadTargets.
func (r *Repository) loadTeamInputs(
	ctx context.Context,
	cfg Config,
	input *Input,
	cutoff pgtype.Date,
	loadTargets func(context.Context, int) ([]PlayerTeam, error),
) error {
	if !supportsTeamEnvironment(cfg.ModelVersion) {
		return nil
	}
	lowerSeason := historyFloorSeason(input.TargetSeason, cfg.LookbackSeasons)
	var err error
	if input.TeamSeasons, err = r.loadTeamSeasons(ctx, input.TargetSeason, lowerSeason, cutoff); err != nil {
		return err
	}
	if input.SkaterClubSeasons, err = r.loadSkaterClubSeasons(ctx, input.TargetSeason, lowerSeason, cutoff); err != nil {
		return err
	}
	input.TargetTeams, err = loadTargets(ctx, input.TargetSeason)
	return err
}

// loadTargetTeams reads each player's club from the target season's
// imported rosters.
func (r *Repository) loadTargetTeams(ctx context.Context, targetSeason int) ([]PlayerTeam, error) {
	rows, err := r.queries.ListProjectionTargetTeams(ctx, int32(targetSeason))
	if err != nil {
		return nil, fmt.Errorf("load projection target teams: %w", err)
	}
	out := make([]PlayerTeam, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlayerTeam{PlayerID: row.PlayerID, TeamID: row.TeamID})
	}
	return out, nil
}

// loadEvaluationTargetTeams reads each skater's first club of a held-out
// season.
func (r *Repository) loadEvaluationTargetTeams(ctx context.Context, targetSeason int) ([]PlayerTeam, error) {
	rows, err := r.queries.ListProjectionEvaluationTargetTeams(ctx, int32(targetSeason))
	if err != nil {
		return nil, fmt.Errorf("load evaluation target teams: %w", err)
	}
	out := make([]PlayerTeam, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlayerTeam{PlayerID: row.PlayerID, TeamID: row.TeamID})
	}
	return out, nil
}

// encodeTeamEnvironment stores a player's adjustment as JSON; nil stays
// SQL NULL.
func encodeTeamEnvironment(env *TeamEnvironment) ([]byte, error) {
	if env == nil {
		return nil, nil
	}
	return json.Marshal(env)
}

func decodeTeamEnvironment(data []byte) (*TeamEnvironment, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var env TeamEnvironment
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	return &env, nil
}
