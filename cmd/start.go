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

	"github.com/pottom/hopscotch/internal/admin"
	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/proxy"
	"github.com/pottom/hopscotch/internal/netcheck"
	"github.com/pottom/hopscotch/internal/security"
	"github.com/pottom/hopscotch/internal/state"
	"github.com/pottom/hopscotch/internal/tunnel"
	"github.com/pottom/hopscotch/internal/updater"
	"github.com/pottom/hopscotch/internal/version"
	"github.com/pottom/hopscotch/internal/vpn"
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
	} else if restartIfRunning {
		// PID file is missing or stale — check if something else holds the admin port.
		if pid, err := pidListeningOnPort(cfg.Admin.Port); err == nil {
			log.Info("found stale process on admin port, stopping it", "pid", pid, "port", cfg.Admin.Port)
			if err := killAndWait(pid, stateMgr); err != nil {
				return err
			}
		}
	}

	// Ensure VPN passwords are available before potentially daemonizing.
	// This prompts interactively when the password is missing and stores it
	// in the OS keychain so subsequent starts are fully unattended.
	if err := ensureVPNPasswords(cfg.VPNs); err != nil {
		return err
	}

	if err := checkVPNSudo(cfg.VPNs); err != nil {
		return err
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

	var vpnGater tunnel.VPNGater
	if len(cfg.VPNs) > 0 {
		vpnGater = vpn.NewManager(cfg.VPNs)
	}

	mgr := tunnel.NewManager(cfg.Tunnels, vpnGater)
	router := proxy.NewRouter(cfg.Proxy.Rules, mgr)
	proxySrv := proxy.NewServer(cfg.Proxy.Bind, cfg.Proxy.Port, router, cfg.Proxy.Username, cfg.Proxy.Password)
	var vpnStatter admin.VPNStatter
	if vpnMgr, ok := vpnGater.(*vpn.Manager); ok {
		vpnStatter = vpnMgr
	}
	adminSrv := admin.NewServer(cfg.Admin.Bind, cfg.Admin.Port, cfg.Proxy.Port, mgr, vpnStatter, router, router, ReadmeContent, cfg, router, mgr, proxySrv.AuthEnabled())

	go config.WatchSIGHUP(ctx, cfg, func(old, next *config.Config) {
		mgr.ApplyConfig(ctx, next.Tunnels)
		router.UpdateRules(next.Proxy.Rules)
		refreshSSHConfig(next)
	})

	log.Info("hopscotch starting",
		"config", cfg.Path,
		"proxy", fmt.Sprintf(":%d", cfg.Proxy.Port),
		"admin", fmt.Sprintf("%s:%d", cfg.Admin.Bind, cfg.Admin.Port),
		"tunnels", len(cfg.Tunnels),
		"vpns", len(cfg.VPNs),
	)
	if cfg.Proxy.Username != "" {
		log.Info("proxy auth enabled", "user", cfg.Proxy.Username)
	}
	if cfg.Admin.Username != "" {
		log.Info("admin auth enabled", "user", cfg.Admin.Username)
	}
	config.LogConfig(cfg)

	if updater.InContainer() {
		log.Info("running in a container — self-update disabled")
	} else {
		if fake := os.Getenv("HOPSCOTCH_FAKE_LATEST_VERSION"); fake != "" {
			version.LatestVersion = fake
			log.Info("update available (faked)", "latest", fake, "current", version.Version)
		} else {
			go func() {
				rel, err := updater.LatestRelease()
				if err != nil {
					return
				}
				if updater.IsNewer(version.Version, rel.TagName) {
					version.LatestVersion = rel.TagName
					log.Info("update available", "latest", rel.TagName, "current", version.Version, "run", "hopscotch update")
				}
			}()
		}
	}

	g, ctx := errgroup.WithContext(ctx)

	if cfg.Admin.ShowPublicIP {
		netcheck.StartPublicIPWatcher(ctx, 60*time.Second)
	}

	if vpnMgr, ok := vpnGater.(*vpn.Manager); ok {
		g.Go(func() error { return vpnMgr.Run(ctx) })
	}
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

// killAndWait sends SIGTERM to pid and waits up to 5 seconds for a clean exit,
// then escalates to SIGKILL. The grace period allows VPN subprocesses to run
// vpnc-script disconnect (route/DNS cleanup) before we force-kill.
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

	log.Warn("graceful shutdown timed out, sending SIGKILL", "pid", pid)
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("process %d did not stop and SIGKILL failed: %w", pid, err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !isRunning(pid) {
			stateMgr.Remove()
			log.Info("previous instance killed")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("process %d did not stop even after SIGKILL", pid)
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

