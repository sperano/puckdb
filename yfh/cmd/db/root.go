package db

import (
	"github.com/spf13/cobra"
)

var DBCmd = &cobra.Command{
	Use:   "db",
	Short: "commands to manage the database",
	Long:  `foo`,
}

func init() {
	//	CacheCmd.AddCommand(read.ReadCmd)
	//CacheCmd.AddCommand(scan.ScanCmd)
}
