package config

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
)

// ReloadFunc is called with the new config after a successful SIGHUP reload.
type ReloadFunc func(old, new *Config)

// WatchSIGHUP listens for SIGHUP and reloads the config at cfg.Path.
// On success it calls fn; on error it logs a warning and keeps the old config.
// Blocks until ctx is cancelled — call in a goroutine.
func WatchSIGHUP(current *Config, fn ReloadFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)

	for range ch {
		next, err := Load(current.Path)
		if err != nil {
			log.Warn("config reload failed, keeping current config", "err", err)
			continue
		}

		warnIfRestartRequired(current, next)
		fn(current, next)

		added, removed := tunnelDiff(current.Tunnels, next.Tunnels)
		log.Info("config reloaded",
			"tunnels_added", added,
			"tunnels_removed", removed,
			"rules_updated", fmt.Sprintf("%v", len(next.Proxy.Rules) != len(current.Proxy.Rules)),
		)

		current = next
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
