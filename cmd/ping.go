package cmd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/tunnel"
)

var (
	pingHost  string
	pingCount int
)

var pingCmd = &cobra.Command{
	Use:   "ping <tunnel>",
	Short: "Test if a tunnel can carry real HTTP traffic",
	Args:  cobra.ExactArgs(1),
	RunE:  runPing,
}

func init() {
	pingCmd.Flags().StringVar(&pingHost, "host", config.DefaultPingHost, "target host to probe")
	pingCmd.Flags().IntVar(&pingCount, "count", config.DefaultPingCount, "number of probes")
	rootCmd.AddCommand(pingCmd)
}

func runPing(cmd *cobra.Command, args []string) error {
	tunnelName := args[0]

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	var tunnelCfg *config.TunnelConfig
	for i, t := range cfg.Tunnels {
		if t.Name == tunnelName {
			tunnelCfg = &cfg.Tunnels[i]
			break
		}
	}
	if tunnelCfg == nil {
		return fmt.Errorf("tunnel %q not found in config", tunnelName)
	}

	t := tunnel.New(*tunnelCfg)

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(config.DefaultPingTunnelWait)*time.Second)
	defer cancel()

	// Start tunnel in background, wait for connection.
	errCh := make(chan error, 1)
	go func() { errCh <- t.Run(ctx) }()

	fmt.Printf("Waiting for tunnel %q to connect...\n", tunnelName)
	deadline := time.Now().Add(time.Duration(config.DefaultPingTunnelWait) * time.Second)
	for time.Now().Before(deadline) {
		if t.Stats().Status == tunnel.StatusConnected {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if t.Stats().Status != tunnel.StatusConnected {
		cancel()
		return fmt.Errorf("tunnel %q did not connect within %ds", tunnelName, config.DefaultPingTunnelWait)
	}

	fmt.Printf("Pinging via tunnel %q → %s\n", tunnelName, pingHost)

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: t.DialContext,
		},
		Timeout: 10 * time.Second,
	}

	var (
		total time.Duration
		minD  = time.Duration(1<<63 - 1)
		maxD  time.Duration
	)

	for i := range pingCount {
		start := time.Now()
		resp, err := httpClient.Get("http://" + pingHost)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("✗ %d/%d  ERROR: %v\n", i+1, pingCount, err)
			log.Debug("ping error", "err", err)
			continue
		}
		resp.Body.Close()

		fmt.Printf("✓ %d/%d  %dms\n", i+1, pingCount, elapsed.Milliseconds())
		total += elapsed
		if elapsed < minD {
			minD = elapsed
		}
		if elapsed > maxD {
			maxD = elapsed
		}
	}

	if total > 0 {
		avg := total / time.Duration(pingCount)
		fmt.Printf("avg: %dms  min: %dms  max: %dms\n",
			avg.Milliseconds(), minD.Milliseconds(), maxD.Milliseconds())
	}

	cancel()
	<-errCh
	return nil
}
