package cmd

import (
	"fmt"
	"log"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
)

var force bool

func init() {
	updateCmd.PersistentFlags().BoolVarP(&force, "force", "f", false, "Always download new copies")
	rootCmd.AddCommand(updateCmd)
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

func DoUpdate(cache *core.Cache) error {
	if err := checkGameNHL(cache); err != nil {
		return err
	}
	if err := checkLeague(cache); err != nil {
		return err
	}
	return nil
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update the local copies",
	Long:  `Update all the local copies of what is available on the Internet`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		fmt.Printf("Config:\n%s", config)
		cache, err := core.NewCache(config)
		if err != nil {
			log.Fatal(err)
		}
		return DoUpdate(cache)
	},
}
