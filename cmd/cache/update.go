package cache

import (
	"fmt"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/cmd/common"
	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
)

var from string
var to string
var force bool
var fantasyGameExplicit bool
var leagueExplicit bool
var teamExplicit int

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "updates the local cache",
	Long:  `Updates the local cache`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		dates, err := common.GetDatesFromCommandLine(config, from, to, args)
		if err != nil {
			return err
		}
		cache, err := core.NewCache(config)
		if err != nil {
			return err
		}
		do_all := true
		if fantasyGameExplicit || leagueExplicit || teamExplicit != 0 {
			do_all = false
		}
		// check fantasy game
		if do_all || fantasyGameExplicit {
			if has, err := cache.HasFantasyGame(); err != nil {
				return err
			} else {
				if force || !has {
					if _, err := cache.DownloadFantasyGame(); err != nil {
						return err
					}
				} else {
					if cfis, err := cache.FindFantasyGame(); err != nil {
						return err
					} else {
						fmt.Printf("%s: Latest='%s'\n", english.Plural(len(cfis), "fantasy game file", ""), cfis[0].Time)
					}
				}
			}
		}
		// check league
		if do_all || leagueExplicit {
			if has, err := cache.HasLeague(); err != nil {
				return err
			} else {
				if force || !has {
					if _, err := cache.DownloadLeague(); err != nil {
						return err
					}
				} else {
					if cfis, err := cache.FindLeague(); err != nil {
						return err
					} else {
						fmt.Printf("%s: Latest='%s'\n", english.Plural(len(cfis), "league file", ""), cfis[0].Time)
					}
				}
			}
		}
		// check team
		if do_all || teamExplicit > 0 {
			for teamID := 1; teamID <= config.TotalTeams; teamID++ {
				if teamExplicit == 0 || teamExplicit == teamID {
					for _, date := range dates {
						if has, err := cache.HasRoster(teamID, date); err != nil {
							return err
						} else {
							if force || !has {
								if _, err := cache.DownloadRoster(teamID, date); err != nil {
									return err
								}
							} else {
								if cfis, err := cache.FindRoster(teamID, date); err != nil {
									return err
								} else {
									fmt.Printf("%s for team %d on %d-%d: Latest='%s'\n", english.Plural(len(cfis), "roster file", ""), teamID, date.Month(), date.Day(), cfis[0].Time)
								}
							}
						}
					}
				}
			}
		}
		return nil
	},
}

func init() {
	updateCmd.PersistentFlags().StringVarP(&from, "from", "F", "", "Update from this date")
	updateCmd.PersistentFlags().StringVarP(&to, "to", "T", "", "Update until and excluding this date")
	updateCmd.PersistentFlags().BoolVarP(&force, "force", "f", false, "Force download")
	updateCmd.PersistentFlags().BoolVarP(&fantasyGameExplicit, "fantasy-game", "g", false, "Specifically for fantasy game")
	updateCmd.PersistentFlags().BoolVarP(&leagueExplicit, "league", "l", false, "Specifically for league")
	updateCmd.PersistentFlags().IntVarP(&teamExplicit, "team", "t", 0, "Specifically for team")
	CacheCmd.AddCommand(updateCmd)
}
