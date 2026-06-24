package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

var (
	foreground       bool
	restartIfRunning bool
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start all tunnels and the proxy router",
	RunE:  runStart,
}

func init() {
	startCmd.Flags().BoolVar(&foreground, "foreground", false, "run in the foreground instead of daemonizing")
	startCmd.Flags().BoolVar(&restartIfRunning, "restart", false, "kill the running instance and restart without prompting")
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

	if pid, err := stateMgr.ReadPID(); err == nil && isRunning(pid) {
		if err := handleAlreadyRunning(pid, stateMgr); err != nil {
			return err
		}
	}

	if !foreground {
		return daemonize()
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
	adminSrv := admin.NewServer(cfg.Admin.Bind, cfg.Admin.Port, cfg.Proxy.Port, mgr, router, router, ReadmeContent)

	go config.WatchSIGHUP(ctx, cfg, func(old, next *config.Config) {
		mgr.ApplyConfig(ctx, next.Tunnels)
		router.UpdateRules(next.Proxy.Rules)
	})

	log.Info("hopscotch starting",
		"proxy", fmt.Sprintf(":%d", cfg.Proxy.Port),
		"admin", fmt.Sprintf("%s:%d", cfg.Admin.Bind, cfg.Admin.Port),
		"tunnels", len(cfg.Tunnels),
	)
	config.LogConfig(cfg)

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error { return mgr.Run(ctx) })
	g.Go(func() error { return proxySrv.ListenAndServe(ctx) })
	g.Go(func() error { return adminSrv.ListenAndServe(ctx) })

	return g.Wait()
}

// handleAlreadyRunning prompts the user (or uses --restart flag) to decide
// whether to kill the existing process before starting a new one.
func handleAlreadyRunning(pid int, stateMgr *state.Manager) error {
	if !restartIfRunning {
		fmt.Printf("hopscotch is already running (PID %d).\n", pid)
		fmt.Print("Restart? [y/N]: ")

		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))

		if answer != "y" && answer != "yes" {
			return fmt.Errorf("aborted")
		}
	}

	return killAndWait(pid, stateMgr)
}

// killAndWait sends SIGTERM to pid and waits up to 5 seconds for it to exit.
func killAndWait(pid int, stateMgr *state.Manager) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	log.Info("stopping running instance", "pid", pid)
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to %d: %w", pid, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !isRunning(pid) {
			stateMgr.Remove()
			log.Info("previous instance stopped")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("process %d did not stop within 5 seconds", pid)
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
	return proc.Signal(syscall.Signal(0)) == nil
}

