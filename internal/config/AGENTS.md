# internal/config

## Purpose

Loads, validates, writes, and hot-reloads `config.yaml`: tunnels, VPNs, proxy rules, admin settings, notification settings. `config.yaml` is exclusively for *declared policy* — see `internal/state/AGENTS.md`'s decision rule for what belongs here vs. in `state.json` vs. nowhere before adding a new persisted field anywhere in the app.

## Ownership

`config.go` (types, `Load`, `applyDefaults`/`ApplyDefaults`, `validate`/`Validate`), `defaults.go` (`DefaultX` constants), `pattern.go` (`ValidatePattern`), `write.go` (`WriteConfig` atomic writer), `reload.go` (SIGHUP + mtime-poll watcher).

## Local Contracts

- `resolvePath` search order: `--config` flag > `$HOPSCOTCH_CONFIG` > `<binary dir>/hopscotch.yaml` > `~/.config/hopscotch/config.yaml` — first existing file wins.
- `ApplyDefaults`/`Validate` are thin exported wrappers around the unexported `applyDefaults`/`validate` that `Load` already runs after parsing — added so a CLI wizard (`cmd/tunnel.go`'s `hopscotch tunnel add`) can normalize and check an in-memory `*Config` it just mutated by hand, in the same order `Load` uses (defaults first, then validate), before calling `WriteConfig`. Any future `<subcommand> add`-style command should reuse these rather than re-deriving normalization/validation.
- `WriteConfig` always round-trips the whole `*Config` through `yaml.Marshal` and prepends a "generated, manual edits will be overwritten" header — hand-written comments in `config.yaml` do not survive an app-triggered write. Anything that writes `config.yaml` (the rules editor `internal/admin/rules.go` and the notifications settings handler `internal/admin/notifications.go`) must accept this.
- `ValidatePattern` (here) and `proxy.matchPattern` (`internal/proxy/pattern.go`) implement the *same* pattern grammar (`*`, `*.suffix`, `prefix.*`, CIDR, exact) as two independent functions — a grammar change must update both (see `internal/proxy/AGENTS.md`).
- Hot-reload (`WatchSIGHUP`) re-`Load()`s and calls the caller's `ReloadFunc`; it does not itself touch running tunnels/VPNs. `tunnel.Manager.ApplyConfig` (invoked from that callback) only adds/removes tunnels by name — changed fields on an existing same-named tunnel are not hot-applied to the already-running `*Tunnel` (see `internal/tunnel/AGENTS.md`). VPNs have no reload path at all; a VPN config change needs a full restart.
- `NotificationsConfig` hot-applies through **two** independent paths, and both must stay wired: `internal/admin/notifications.go`'s `PUT /api/notifications` (UI-driven edit) writes it to disk *and* pushes it straight into the running `*notify.Notifier` via `SetConfig`; `cmd/start.go`'s `WatchSIGHUP` callback also calls `notifier.SetConfig(next.Notifications)` (hand-edited `config.yaml` + SIGHUP). Unlike tunnels/VPNs, there's no partial-apply gap here — `Notifier.SetConfig` always replaces the whole struct atomically, so both paths can converge on the same call. A regression here is silent: `/status` (which reads `s.notifyCtl.Config()`, not `s.cfg`) keeps reporting the stale value until whichever path is broken gets fixed (see `internal/admin/AGENTS.md`).

## Work Guidance

(none beyond the above)

## Verification

`go test ./internal/config/...`
