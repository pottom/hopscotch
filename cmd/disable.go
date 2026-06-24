package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var disableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Print shell commands to deactivate the SOCKS5 proxy in the current shell",
	Long: `Outputs shell commands that restore the proxy environment to its state
before 'hopscotch enable' was called. Run with eval:

  eval $(hopscotch disable)`,
	RunE: runDisable,
}

func init() {
	rootCmd.AddCommand(disableCmd)
}

func runDisable(_ *cobra.Command, _ []string) error {
	warnIfNoShellInit()
	for _, v := range shellVars {
		prev, ok := os.LookupEnv("_HOPSCOTCH_PREV_" + v)
		if !ok || prev == unsetSentinel {
			fmt.Printf("unset %s\n", v)
		} else {
			fmt.Printf("export %s=%s\n", v, shellQuote(prev))
		}
		fmt.Printf("unset _HOPSCOTCH_PREV_%s\n", v)
	}
	fmt.Println("unset HOPSCOTCH_ACTIVE")

	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
	fmt.Fprintf(os.Stderr, "%s\n", muted.Render("proxy disabled"))
	return nil
}
