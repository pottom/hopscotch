package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"hopscotch/internal/admin"
	"hopscotch/internal/config"
	"hopscotch/internal/proxy"
	"hopscotch/internal/security"
	"hopscotch/internal/state"
	"hopscotch/internal/tunnel"
)

var foreground bool

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start all tunnels and the proxy router",
	RunE:  runStart,
}

func init() {
	startCmd.Flags().BoolVar(&foreground, "foreground", false, "run in the foreground (default in containers)")
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	if err := checkKeys(cfg); err != nil {
		return err
	}

	stateMgr, err := state.NewManager()
	if err != nil {
		return fmt.Errorf("state manager: %w", err)
	}

	// Detect if already running.
	if pid, err := stateMgr.ReadPID(); err == nil {
		if isRunning(pid) {
			return fmt.Errorf("hopscotch is already running (PID %d)", pid)
		}
	}

	if err := stateMgr.WritePID(); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer stateMgr.Remove()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	mgr := tunnel.NewManager(cfg.Tunnels)
	router := proxy.NewRouter(cfg.Proxy.Rules, mgr)
	proxySrv := proxy.NewServer(cfg.Proxy.Port, router)
	adminSrv := admin.NewServer(cfg.Admin.Bind, cfg.Admin.Port, mgr)

	// Hot-reload on SIGHUP.
	go config.WatchSIGHUP(cfg, func(old, next *config.Config) {
		mgr.ApplyConfig(ctx, next.Tunnels)
		router.UpdateRules(next.Proxy.Rules)
	})

	log.Info("hopscotch starting",
		"proxy", fmt.Sprintf(":%d", cfg.Proxy.Port),
		"admin", fmt.Sprintf("%s:%d", cfg.Admin.Bind, cfg.Admin.Port),
		"tunnels", len(cfg.Tunnels),
	)

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error { return mgr.Run(ctx) })
	g.Go(func() error { return proxySrv.ListenAndServe(ctx) })
	g.Go(func() error { return adminSrv.ListenAndServe(ctx) })

	return g.Wait()
}

func checkKeys(cfg *config.Config) error {
	if os.Getenv("HOPSCOTCH_INSECURE_SKIP_KEY_CHECK") == "true" {
		log.Warn("SSH key permission check disabled (HOPSCOTCH_INSECURE_SKIP_KEY_CHECK=true)")
		return nil
	}

	var paths []string
	for _, t := range cfg.Tunnels {
		if t.IdentityFile != "" {
			paths = append(paths, t.IdentityFile)
		}
	}

	return security.CheckKeyFiles(paths)
}

func isRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, signal 0 checks if the process exists without killing it.
	return proc.Signal(syscall.Signal(0)) == nil
}
