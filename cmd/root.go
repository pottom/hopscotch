// Package cmd contains all hopscotch CLI commands.
package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"hopscotch/internal/logger"
)

var (
	configPath    string
	verbose       bool
	logFile       string
	ReadmeContent []byte // set by main via docs.go embed
)

var rootCmd = &cobra.Command{
	Use:          "hopscotch",
	Short:        "SSH tunnel manager with built-in SOCKS5 proxy router",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// these commands only write to stdout, no logger needed.
		switch cmd.Name() {
		case "version", "enable", "disable", "shell-init", "update", "password":
			return nil
		}
		return logger.Init(verbose, logFile)
	},
}

// Execute is the entry point called from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "path to config file")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "enable debug logging")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "also write logs to this file")
}
