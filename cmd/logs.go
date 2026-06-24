package cmd

import (
	"bufio"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Stream live log output from the running daemon",
	Long: `Connects to the running hopscotch daemon and streams its log output.

  hopscotch logs                   # stream live
  hopscotch logs | grep tunnel=prod
  hopscotch logs | grep -i error`,
	RunE: runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)
}

func runLogs(_ *cobra.Command, _ []string) error {
	adminPort, err := resolveAdminPort()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/logs/stream", adminPort)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("hopscotch is not running")
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// SSE format: "data: <payload>\n" — strip the prefix.
		if len(line) > 6 && line[:6] == "data: " {
			fmt.Println(line[6:])
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("log stream interrupted: %w", err)
	}
	return nil
}
