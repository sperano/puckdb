package cache

import (
	"fmt"

	"github.com/ericsperano/yfh/cmd/common"
	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
)

func init() {
	var from string
	var to string
	var force bool
	var fantasyGameExplicit bool
	var leagueExplicit bool
	var teamExplicit int

	var importCmd = &cobra.Command{
		Use:   "import",
		Short: "imports cache into the database",
		Long:  `Imports cache into the database`,
		RunE: func(cmd *cobra.Command, args []string) error {
			config := core.GetConfig(common.ConfigPath)
			dates, err := common.GetDatesFromCommandLine(config, from, to, args)
			if err != nil {
				return err
			}
			cache, err := core.NewCache(config)
			if err != nil {
				return err
			}
			db, err := core.NewDB(config)
			if err != nil {
				return err
			}
			do_all := true
			if fantasyGameExplicit || leagueExplicit || teamExplicit != 0 {
				do_all = false
			}
			// check fantasy game
			if do_all || fantasyGameExplicit {
				if has, err := db.HasFantasyGame(); err != nil {
					return err
				} else {
					if force || !has {
						xmlfg, err := cache.GetFantasyGame()
						if err != nil {
							return err
						}
						if err := db.Create(xmlfg.ToGormModel()).Error; err != nil {
							return err
						}
						fmt.Println("Fantasy game imported.")
					} else {
						fmt.Println("Fantasy game already imported,")
					}
				}
			}
			// check league
			if do_all || leagueExplicit {
				if has, err := db.HasLeague(); err != nil {
					return err
				} else {
					if force || !has {
						xmll, err := cache.GetLeague()
						if err != nil {
							return err
						}
						if err := db.Create(xmll.ToGormModel()).Error; err != nil {
							return err
						}
						fmt.Println("League imported.")
					} else {
						fmt.Println("League already imported,")
					}
				}
			}
			// check team
			if do_all || teamExplicit > 0 {
				for teamID := 1; teamID <= config.TotalTeams; teamID++ {
					if teamExplicit == 0 || teamExplicit == teamID {
						for _, date := range dates {
							xmlroster, err := cache.GetRoster(teamID, date)
							if err != nil {
								return err
							}
							for _, xmlplayer := range xmlroster.Roster.Players.Slice {
								if has, err := db.HasPlayer(xmlplayer.ID); err != nil {
									return err
								} else {
									if force || !has {
										if err := db.Create(xmlplayer.ToGormModel()).Error; err != nil {
											return err
										}
										fmt.Printf("Player %s imported.\n", xmlplayer.Name.Full)
									} else {
										fmt.Printf("Player %s already imported.\n", xmlplayer.Name.Full)
									}
								}
							}
							/*
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
							*/
						}
					}
				}
			}
			return nil
		},
	}
	importCmd.PersistentFlags().StringVarP(&from, "from", "F", "", "Update from this date")
	importCmd.PersistentFlags().StringVarP(&to, "to", "T", "", "Update until and excluding this date")
	importCmd.PersistentFlags().BoolVarP(&force, "force", "f", false, "Force download")
	importCmd.PersistentFlags().BoolVarP(&fantasyGameExplicit, "fantasy-game", "g", false, "Specifically for fantasy game")
	importCmd.PersistentFlags().BoolVarP(&leagueExplicit, "league", "l", false, "Specifically for league")
	importCmd.PersistentFlags().IntVarP(&teamExplicit, "team", "t", 0, "Specifically for team")
	CacheCmd.AddCommand(importCmd)
}
