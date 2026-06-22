package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"hopscotch/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hopscotch %s\n", version.String())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
