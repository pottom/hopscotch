package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"hopscotch/internal/config"
)

// shellVars are the proxy env vars we manage, uppercase and lowercase pairs.
var shellVars = []string{
	"HTTP_PROXY", "http_proxy",
	"HTTPS_PROXY", "https_proxy",
	"NO_PROXY", "no_proxy",
}

const unsetSentinel = "__HOPSCOTCH_UNSET__"

var enableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Print shell commands to activate the SOCKS5 proxy in the current shell",
	Long: `Outputs shell export statements. Run with eval:

  eval $(hopscotch enable)

To show an indicator in your prompt, add to your .zshrc / .bashrc:

  PS1='${HOPSCOTCH_ACTIVE:+$HOPSCOTCH_ACTIVE }your_existing_prompt'`,
	RunE: runEnable,
}

func init() {
	rootCmd.AddCommand(enableCmd)
}

func warnIfNoShellInit() {
	if os.Getenv("HOPSCOTCH_SHELL_INIT") == "" {
		warn := lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Bold(true)
		muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
		fmt.Fprintf(os.Stderr, "%s shell-init not loaded — proxy vars won't apply to this shell\n",
			warn.Render("⚠"),
		)
		fmt.Fprintf(os.Stderr, "  %s\n",
			muted.Render(`add 'eval "$(hopscotch shell-init)"' to your ~/.zshrc or ~/.bashrc`),
		)
	}
}

func runEnable(_ *cobra.Command, _ []string) error {
	warnIfNoShellInit()

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	proxyURL := fmt.Sprintf("socks5h://127.0.0.1:%d", cfg.Proxy.Port)

	// Save current values (or sentinel if unset) so disable can restore them.
	for _, v := range shellVars {
		val, ok := os.LookupEnv(v)
		if ok {
			fmt.Printf("export _HOPSCOTCH_PREV_%s=%s\n", v, shellQuote(val))
		} else {
			fmt.Printf("export _HOPSCOTCH_PREV_%s=%s\n", v, unsetSentinel)
		}
	}

	// Set proxy vars.
	for _, v := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		fmt.Printf("export %s=%s\n", v, shellQuote(proxyURL))
	}
	if cfg.Proxy.NoProxy != "" {
		fmt.Printf("export NO_PROXY=%s\n", shellQuote(cfg.Proxy.NoProxy))
		fmt.Printf("export no_proxy=%s\n", shellQuote(cfg.Proxy.NoProxy))
	}

	fmt.Printf("export HOPSCOTCH_ACTIVE=%s\n", shellQuote(cfg.Proxy.ShellIcon))

	// Human-readable confirmation to stderr — not captured by eval $().
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true)
	muted  := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
	fmt.Fprintf(os.Stderr, "%s proxy enabled  %s\n",
		accent.Render(cfg.Proxy.ShellIcon),
		muted.Render(proxyURL),
	)
	if cfg.Proxy.NoProxy != "" {
		fmt.Fprintf(os.Stderr, "  %s\n", muted.Render("NO_PROXY="+cfg.Proxy.NoProxy))
	}
	return nil
}

// shellQuote wraps s in single quotes, escaping any single quotes inside.
func shellQuote(s string) string {
	result := "'"
	for _, ch := range s {
		if ch == '\'' {
			result += "'\\''"
		} else {
			result += string(ch)
		}
	}
	return result + "'"
}
