package cache

import (
	"fmt"
	"time"

	"github.com/ericsperano/yfh/cmd/common"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/xmlmodel"
	"github.com/spf13/cobra"
)

func init() {
	var from string
	var to string
	var force bool
	var fantasyGameExplicit bool
	var leagueExplicit bool
	var teamExplicit int
	var verbose bool
	doAll := true

	checkFantasyGame := func(cache *core.Cache, db *core.DB) error {
		if doAll || fantasyGameExplicit {
			if has, err := db.HasFantasyGame(); err != nil {
				return err
			} else {
				if force || !has {
					xmlfg, err := cache.GetFantasyGame()
					if err != nil {
						return err
					}
					if err := db.Create(xmlfg.ToFantasyGameModel()).Error; err != nil {
						return err
					}
					fmt.Println("Fantasy game imported.")
				} else {
					if verbose {
						fmt.Println("Fantasy game already imported.")
					}
				}
			}
		}
		return nil
	}

	checkLeague := func(cache *core.Cache, db *core.DB) error {
		if doAll || leagueExplicit {
			if has, err := db.HasLeague(); err != nil {
				return err
			} else {
				if force || !has {
					xmll, err := cache.GetLeague()
					if err != nil {
						return err
					}
					if err := db.Create(xmll.ToLeagueModel()).Error; err != nil {
						return err
					}
					fmt.Println("League imported.")
				} else {
					if verbose {
						fmt.Println("League already imported.")
					}
				}
			}
		}
		return nil
	}

	checkTeam := func(cache *core.Cache, db *core.DB, xmlteam *xmlmodel.Team) error {
		if has, err := db.HasTeam(xmlteam.ID); err != nil {
			return err
		} else {
			if force || !has {
				model := xmlteam.ToTeamModel()
				if err := db.Create(model).Error; err != nil {
					return err
				}
				fmt.Printf("Team %s imported.\n", xmlteam.Name)
			} else {
				if verbose {
					fmt.Printf("Team %s already imported.\n", xmlteam.Name)
				}
			}
		}
		return nil
	}

	checkPlayer := func(cache *core.Cache, db *core.DB, xmlplayer *xmlmodel.Player) error {
		if has, err := db.HasPlayer(xmlplayer.ID); err != nil {
			return err
		} else {
			if force || !has {
				if model, err := xmlplayer.ToPlayerModel(); err != nil {
					return err
				} else {
					if err := db.Create(model).Error; err != nil {
						return err
					}
					fmt.Printf("Player %s imported.\n", xmlplayer.Name.Full)
				}
			} else {
				if verbose {
					fmt.Printf("Player %s already imported.\n", xmlplayer.Name.Full)
				}
			}
		}
		return nil
	}

	checkRosterPlayer := func(cache *core.Cache, db *core.DB, xmlteam *xmlmodel.Team, xmlplayer *xmlmodel.Player, date time.Time) error {
		month := date.Month()
		day := date.Day()
		if has, err := db.HasRosterPlayer(xmlteam.ID, xmlplayer.ID, month, day); err != nil {
			return err
		} else {
			if force || !has {
				if model, err := xmlteam.ToRosterPlayerModel(xmlplayer); err != nil {
					return err
				} else {
					model.TeamID = uint(xmlteam.ID)
					if err := db.Create(model).Error; err != nil {
						return err
					}
					fmt.Printf("Roster for %s on %d-%d imported.\n", xmlplayer.Name.Full, month, day)
				}

			} else {
				if verbose {
					fmt.Printf("Roster for %s on %d-%d already imported.\n", xmlplayer.Name.Full, month, day)
				}
			}
		}
		return nil
	}

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
			if fantasyGameExplicit || leagueExplicit || teamExplicit != 0 {
				doAll = false
			}
			// check fantasy game
			if err := checkFantasyGame(cache, db); err != nil {
				return err
			}
			// check league
			if err := checkLeague(cache, db); err != nil {
				return err
			}
			// check team
			if doAll || teamExplicit > 0 {
				for _, teamID := range config.TeamIDs {
					if teamExplicit == 0 || teamExplicit == teamID {
						for _, date := range dates {
							xmlteam, err := cache.GetRoster(teamID, date)
							if err != nil {
								return err
							}
							if err := checkTeam(cache, db, xmlteam); err != nil {
								return err
							}
							for _, xmlplayer := range xmlteam.Roster.Players.Slice {
								if err := checkPlayer(cache, db, &xmlplayer); err != nil {
									return err
								}
								if err := checkRosterPlayer(cache, db, xmlteam, &xmlplayer, date); err != nil {
									return err
								}
							}
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
	importCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "More verbose output")
	CacheCmd.AddCommand(importCmd)
}
