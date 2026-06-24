package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"hopscotch/internal/updater"
	"hopscotch/internal/version"
)

var checkOnly bool

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for a newer release and update the binary",
	Long: `Fetches the latest release from GitHub and replaces the running binary
if a newer version is available.

  hopscotch update           # check and update
  hopscotch update --check   # check only, do not download`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&checkOnly, "check", false, "only check for updates, do not download")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(_ *cobra.Command, _ []string) error {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true)
	muted  := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
	good   := lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399"))
	bad    := lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171"))

	if updater.InContainer() {
		fmt.Println(bad.Render("running in a container — self-update is disabled"))
		fmt.Println(muted.Render("update the container image instead"))
		return nil
	}

	fmt.Print(muted.Render("checking for updates… "))
	rel, err := updater.LatestRelease()
	if err != nil {
		fmt.Println()
		return fmt.Errorf("checking for updates: %w", err)
	}

	if !updater.IsNewer(version.Version, rel.TagName) {
		fmt.Println(good.Render("already up to date (" + version.Version + ")"))
		return nil
	}

	fmt.Println(accent.Render(rel.TagName + " available") + muted.Render(" (current: "+version.Version+")"))

	if checkOnly {
		fmt.Println(muted.Render("run without --check to update"))
		return nil
	}

	url := rel.AssetURL()
	if url == "" {
		return fmt.Errorf("no release asset found for %s/%s", "hopscotch-darwin/linux", "amd64/arm64")
	}

	self, err := updater.SelfPath()
	if err != nil {
		return fmt.Errorf("finding binary path: %w", err)
	}

	fmt.Print(muted.Render("downloading " + rel.TagName + "… "))
	if err := updater.Download(url, self); err != nil {
		// If permission denied, hint about sudo.
		if os.IsPermission(err) {
			fmt.Println(bad.Render("permission denied"))
			fmt.Println(muted.Render("try: sudo hopscotch update"))
			return nil
		}
		fmt.Println()
		return err
	}

	fmt.Println(good.Render("done"))
	fmt.Printf("  updated %s → %s\n", muted.Render(version.Version), accent.Render(rel.TagName))
	log.Info("binary updated", "from", version.Version, "to", rel.TagName, "path", self)
	return nil
}
