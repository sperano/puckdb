package cache

import (
	"fmt"

	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
)

func init() {
	CacheCmd.AddCommand(fantasyGameCmd)
}

var fantasyGameCmd = &cobra.Command{
	Use:   "fantasy-game",
	Short: "foo",
	Long:  `foo`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		cache, err := core.NewCache(config)
		if err != nil {
			return err
		}
		game, err := cache.GetFantasyGame()
		if err != nil {
			return err
		}
		fmt.Print("=== FantasyGame ===\n")
		fmt.Printf("ID:                    %d\n", game.ID)
		fmt.Printf("Key:                   %d\n", game.Key)
		fmt.Printf("Name:                  %s\n", game.Name)
		fmt.Printf("Code:                  %s\n", game.Code)
		fmt.Printf("Type:                  %s\n", game.Type)
		fmt.Printf("URL:                   %s\n", game.URL)
		fmt.Printf("Season:                %d\n", game.Season)
		fmt.Printf("Is Registration Over:  %v\n", game.IsRegistrationOver)
		fmt.Printf("Is Game Over:          %v\n", game.IsGameOver)
		fmt.Printf("Is Offseason:          %v\n", game.IsOffseason)
		return nil
	},
}
