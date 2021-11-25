package cmd

/*
var force bool

func init() {
	updateCmd.PersistentFlags().BoolVarP(&force, "force", "f", false, "Always download new copies")
	//rootCmd.AddCommand(updateCmd)
}

func checkGameNHL(cache *core.Cache) error {
	cfis, err := cache.FindGameNHL()
	if err != nil {
		return err
	}
	if force || len(cfis) == 0 {
		fmt.Printf("Downloading game NHL file...\n")
		if err = cache.DownloadGameNHL(); err != nil {
			return err
		}
		return checkGameNHL(cache)
	}
	fmt.Printf("%s. Latest='%s'\n", english.Plural(len(cfis), "game NHL file", ""), cfis[0].Time)
	return nil
}

func checkLeague(cache *core.Cache) error {
	cfis, err := cache.FindLeague()
	if err != nil {
		return err
	}
	if force || len(cfis) == 0 {
		fmt.Printf("Downloading league file...\n")
		if err = cache.DownloadLeague(); err != nil {
			return err
		}
		return checkLeague(cache)
	}
	fmt.Printf("%s. Latest='%s'\n", english.Plural(len(cfis), "league file", ""), cfis[0].Time)
	return nil
}

func checkTeam(cache *core.Cache, teamID int) error {
	cfis, err := cache.FindTeam(teamID)
	if err != nil {
		return err
	}
	if force || len(cfis) == 0 {
		fmt.Printf("Downloading team %d file...\n", teamID)
		if err = cache.DownloadTeam(teamID); err != nil {
			return err
		}
		return checkTeam(cache, teamID)
	}
	fmt.Printf("%s for team %d. Latest='%s'\n", english.Plural(len(cfis), "team file", ""), teamID, cfis[0].Time)
	return nil
}

func checkRosters(config *core.Config, cache *core.Cache, teamID int) error {
	now := time.Now()
	t := time.Date(config.SeasonStartYear, cache.Config.SeasonStartMonth, config.SeasonStartDay, 0, 0, 0, 0, time.UTC)
	for {
		if err := checkRoster(config, cache, teamID, t); err != nil {
			return err
		}
		t = t.AddDate(0, 0, 1)
		if t.Day() == now.Day() && t.Month() == now.Month() && t.Year() == now.Year() {
			break
		}
	}
	return nil
}

func checkRoster(config *core.Config, cache *core.Cache, teamID int, date time.Time) error {
	cfis, err := cache.FindTeamRoster(teamID, date)
	if err != nil {
		return err
	}
	if force || len(cfis) == 0 {
		fmt.Printf("Downloading team %d roster for %s...\n", teamID, date)
		if err = cache.DownloadTeamRoster(teamID, date); err != nil {
			return err
		}
		return checkRoster(config, cache, teamID, date)
	}
	fmt.Printf("%s for team %d on %s. Latest='%s'\n", english.Plural(len(cfis), "team file", ""), teamID, date, cfis[0].Time)
	return nil
}

func DoUpdate(config *core.Config, cache *core.Cache) error {
	if err := checkGameNHL(cache); err != nil {
		return err
	}
	if err := checkLeague(cache); err != nil {
		return err
	}
	for i := 1; i <= config.TotalTeams; i++ {
		if err := checkTeam(cache, i); err != nil {
			return err
		}
		if err := checkRosters(config, cache, i); err != nil {
			return err
		}
	}
	return nil
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update the local copies",
	Long:  `Update all the local copies of what is available on the Internet`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		fmt.Printf("Config:\n---\n%+v---\n", config)
		cache, err := core.NewCache(config)
		if err != nil {
			log.Fatal(err)
		}
		game, err := cache.GetFantasyGame()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%v\n", game)
		team, err := cache.GetTeam(7)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%v\n", team)
		//return DoUpdate(config, cache)
		return nil
	},
}
*/
