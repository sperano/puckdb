package read

import (
	"fmt"
	"log"

	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
)

func init() {
	ReadCmd.AddCommand(fantasyGameCmd)
}

var fantasyGameCmd = &cobra.Command{
	Use:   "fantasy-game",
	Short: "foo",
	Long:  `foo`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		cache, err := core.NewCache(config)
		if err != nil {
			log.Fatal(err)
		}
		game, err := cache.GetFantasyGame()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ID:   %d\n", game.ID)
		fmt.Printf("Key:  %d\n", game.Key)
		return nil
	},
}
