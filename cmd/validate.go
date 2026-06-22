package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"hopscotch/internal/config"
	"hopscotch/internal/security"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the config file without connecting",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}

		log.Info("config loaded", "path", cfg.Path, "tunnels", len(cfg.Tunnels))

		if os.Getenv("HOPSCOTCH_INSECURE_SKIP_KEY_CHECK") != "true" {
			var keyPaths []string
			for _, t := range cfg.Tunnels {
				if t.IdentityFile != "" {
					keyPaths = append(keyPaths, t.IdentityFile)
				}
			}
			if err := security.CheckKeyFiles(keyPaths); err != nil {
				return fmt.Errorf("key permission check failed:\n%w", err)
			}
			log.Info("SSH key permissions OK")
		}

		fmt.Println("Config is valid.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(validateCmd)
}
