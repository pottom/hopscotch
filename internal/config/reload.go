package config

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
)

// ReloadFunc is called with the new config after a successful reload.
type ReloadFunc func(old, new *Config)

// WatchSIGHUP listens for SIGHUP and watches the config file for changes.
// On either trigger it reloads the config and calls fn.
// Blocks until ctx is cancelled — call in a goroutine.
func WatchSIGHUP(ctx context.Context, current *Config, fn ReloadFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)

	var mtime time.Time
	if fi, err := os.Stat(current.Path); err == nil {
		mtime = fi.ModTime()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			if next := applyReload(current, fn, "SIGHUP"); next != nil {
				current = next
				mtime = currentMtime(current.Path)
			}
		case <-ticker.C:
			fi, err := os.Stat(current.Path)
			if err != nil || !fi.ModTime().After(mtime) {
				continue
			}
			mtime = fi.ModTime()
			if next := applyReload(current, fn, "file change"); next != nil {
				current = next
			}
		}
	}
}

func applyReload(current *Config, fn ReloadFunc, via string) *Config {
	next, err := Load(current.Path)
	if err != nil {
		log.Warn("config reload failed, keeping current config", "err", err)
		return nil
	}

	warnIfRestartRequired(current, next)
	fn(current, next)

	added, removed := tunnelDiff(current.Tunnels, next.Tunnels)
	log.Info("config reloaded",
		"via", via,
		"tunnels_added", added,
		"tunnels_removed", removed,
	)
	LogConfig(next)
	return next
}

func currentMtime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// LogConfig logs the tunnel and route config in the same format as startup.
func LogConfig(cfg *Config) {
	home, _ := os.UserHomeDir()
	for _, t := range cfg.Tunnels {
		keyField := "agent"
		if t.IdentityFile != "" {
			key := t.IdentityFile
			if home != "" && strings.HasPrefix(key, home) {
				key = "~" + key[len(home):]
			}
			keyField = key
		}
		log.Info("tunnel",
			"name", t.Name,
			"host", fmt.Sprintf("%s:%d", t.Host, t.Port),
			"user", t.User,
			"socks5", fmt.Sprintf(":%d", t.LocalPort),
			"key", keyField,
		)
	}
	for _, r := range cfg.Proxy.Rules {
		log.Info("route", "pattern", r.Pattern, "target", r.Target)
	}
}

func warnIfRestartRequired(old, new *Config) {
	if old.Proxy.Port != new.Proxy.Port {
		log.Warn("proxy.port changed – restart required to apply", "old", old.Proxy.Port, "new", new.Proxy.Port)
	}
	if old.Admin.Port != new.Admin.Port {
		log.Warn("admin.port changed – restart required to apply", "old", old.Admin.Port, "new", new.Admin.Port)
	}
}

func tunnelDiff(old, new []TunnelConfig) (added, removed int) {
	oldNames := map[string]bool{}
	for _, t := range old {
		oldNames[t.Name] = true
	}
	newNames := map[string]bool{}
	for _, t := range new {
		newNames[t.Name] = true
	}
	for name := range newNames {
		if !oldNames[name] {
			added++
		}
	}
	for name := range oldNames {
		if !newNames[name] {
			removed++
		}
	}
	return added, removed
}
