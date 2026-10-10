package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/spf13/cobra"
)

// preNHLCupOpponentIDs are the NHL API team IDs of the PCHA/WCHL/WHL clubs
// that met NHL teams in the 1918–1926 Stanley Cup finals. season_teams
// seeds those clubs under synthetic IDs 70–74 (000001_init), so the API IDs
// on their games never get rows; db check-teams treats them as known gaps.
var preNHLCupOpponentIDs = []int64{7284, 7285, 7286, 7287, 7288, 7289}

// dbCheckTeamsFlagGroups lists every flag group `db check-teams` exposes.
var dbCheckTeamsFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
}

func cmdDBCheckTeams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check-teams",
		Short: "Report games whose teams have no season_teams row",
		Long: `Report regular-season and playoff games that reference a team_id with no
season_teams row for that season. Games queries join season_teams on both
sides, so such games are silently missing from schedules and game logs.
Exits non-zero when any are found.`,
		PreRunE: bindFlagsPreRunE(dbCheckTeamsFlagGroups...),
		RunE:    runDBCheckTeams,
	}
	config.InitFlags(cmd.Flags(), dbCheckTeamsFlagGroups...)
	return cmd
}

func runDBCheckTeams(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	pool, err := openPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	rows, err := sqlcdb.New(pool).ListGameTeamsWithoutSeasonTeam(ctx, preNHLCupOpponentIDs)
	if err != nil {
		return fmt.Errorf("list game teams without season_teams row: %w", err)
	}
	return reportOrphanGameTeams(cmd.OutOrStdout(), rows)
}

// reportOrphanGameTeams prints one line per orphaned (season, team, game
// type) and returns an error when there is at least one.
func reportOrphanGameTeams(w io.Writer, rows []sqlcdb.ListGameTeamsWithoutSeasonTeamRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "All regular-season and playoff game teams have season_teams rows.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEASON\tTEAM_ID\tGAME_TYPE\tGAMES")
	var games int64
	for _, r := range rows {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%d\n", r.Season, r.TeamID, r.GameType, r.Games)
		games += r.Games
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	return fmt.Errorf("%d game-team references across %d (season, team, game type) groups have no season_teams row", games, len(rows))
}
