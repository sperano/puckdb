package worker

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/date"
	"time"
)

func ImportTeamActivity(ctx context.Context, teamID uint, force bool) (string, error) {
	log.Debug().Uint("teamID", teamID).Bool("force", force).Msg("ImportTeamActivity")
	//ctx = context.WithValue(ctx, core.CtxUser, core.DefaultUser)
	//t, err := core.ImportTeam(ctx, core.GlobalYFH(), teamID, force)
	//if err != nil {
	//	return "", err
	//}
	return fmt.Sprintf("imported team %d", teamID), nil
}

func ImportGameActivity(ctx context.Context, season int, day time.Time, gamelink string, force bool) (string, error) {
	dayStr := date.ToYearString(day)
	log.Debug().Int("season", season).Str("day", dayStr).Str("gamelink", gamelink).Bool("force", force).Msg("ImportFantasyGameActivity")
	//ctx = context.WithValue(ctx, core.CtxUser, core.DefaultUser)
	//_, _, _, err := core.ImportGame(ctx, core.GlobalYFH(), season, date, gamelink)
	//if err != nil {
	//	return "", err
	//}
	return fmt.Sprintf("imported game %s %s", day, gamelink), nil
}

func EnsureGamesListOnDayActivity(ctx context.Context, season int, day time.Time, force bool) ([]string, error) {
	//dateStr := core.ToYearString(date)
	//log.Debug().Int("season", season).Str("date", dateStr).Bool("force", force).Msg("EnsureGamesListOnDayActivity")
	//ctx = context.WithValue(ctx, core.CtxUser, core.DefaultUser)
	//yfh := core.GlobalYFH()
	//file, err := core.EnsureRecentGamesListOnDay(ctx, yfh, season, force, date)
	//if err != nil {
	//	return nil, fmt.Errorf("ensure recent games list for date: %s: %w", core.ToYearString(date), err)
	//}
	//return core.ParseGameLinks(path.Join(yfh.Local.Path_, core.Path(file)))
	return nil, config.ErrNotImplementedYet
}

func ImportRosterPlayersForTeamOnDayActivity(ctx context.Context, season int, teamID uint, day time.Time, force bool) (string, error) {
	dayStr := date.ToYearString(day)
	log.Debug().Uint("teamID", teamID).Str("day", dayStr).Bool("force", force).
		Msg("ImportRosterPlayersForTeamOnDayActivity")
	//ctx = context.WithValue(ctx, core.CtxUser, core.DefaultUser)
	//yfh := core.GlobalYFH()
	//if _, err := core.ImportRosterPlayersForTeamOnDay(ctx, yfh, season, teamID, day, force); err != nil {
	//	return "", err
	//}
	return fmt.Sprintf("imported roster players team: %d %d %s", season, teamID, dayStr), nil
}

func ImportTeamSummaryForTeamOnDayActivity(ctx context.Context, season int, teamID uint, day time.Time, force bool) (string, error) {
	dayStr := date.ToYearString(day)
	log.Debug().Int("season", season).Uint("teamID", teamID).Str("day", dayStr).Bool("force", force).
		Msg("ImportTeamSummaryForTeamOnDayActivity")
	//ctx = context.WithValue(ctx, core.CtxUser, core.DefaultUser)
	//yfh := core.GlobalYFH()
	//if _, err := core.ImportTeamSummaryForTeamOnDay(ctx, yfh, season, teamID, date, force); err != nil {
	//	return "", err
	//}
	return fmt.Sprintf("imported roster players team: %d %s", teamID, dayStr), nil
}
