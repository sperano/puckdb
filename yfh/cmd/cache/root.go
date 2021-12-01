package cache

import (
	"github.com/spf13/cobra"
)

var CacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "commands to manage the local cache of xml files",
	Long:  `foo`,
}

func init() {
	//	CacheCmd.AddCommand(read.ReadCmd)
	//CacheCmd.AddCommand(scan.ScanCmd)
}
