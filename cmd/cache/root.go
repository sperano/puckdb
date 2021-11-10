package cache

import (
	"github.com/ericsperano/yfh/cmd/cache/read"
	"github.com/spf13/cobra"
)

var CacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "foo",
	Long:  `foo`,
}

func init() {
	CacheCmd.AddCommand(read.ReadCmd)
}
