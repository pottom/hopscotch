# internal/config

## Purpose

Loads, validates, writes, and hot-reloads `config.yaml`: tunnels, VPNs, proxy rules, admin settings.

## Ownership

`config.go` (types, `Load`, `applyDefaults`, `validate`), `defaults.go` (`DefaultX` constants), `pattern.go` (`ValidatePattern`), `write.go` (`WriteConfig` atomic writer), `reload.go` (SIGHUP + mtime-poll watcher).

## Local Contracts

- `resolvePath` search order: `--config` flag > `$HOPSCOTCH_CONFIG` > `<binary dir>/hopscotch.yaml` > `~/.config/hopscotch/config.yaml` — first existing file wins.
- `WriteConfig` always round-trips the whole `*Config` through `yaml.Marshal` and prepends a "generated, manual edits will be overwritten" header — hand-written comments in `config.yaml` do not survive an app-triggered write. Anything that writes `config.yaml` (currently only the rules editor, `internal/admin/rules.go`) must accept this.
- `ValidatePattern` (here) and `proxy.matchPattern` (`internal/proxy/pattern.go`) implement the *same* pattern grammar (`*`, `*.suffix`, `prefix.*`, CIDR, exact) as two independent functions — a grammar change must update both (see `internal/proxy/AGENTS.md`).
- Hot-reload (`WatchSIGHUP`) re-`Load()`s and calls the caller's `ReloadFunc`; it does not itself touch running tunnels/VPNs. `tunnel.Manager.ApplyConfig` (invoked from that callback) only adds/removes tunnels by name — changed fields on an existing same-named tunnel are not hot-applied to the already-running `*Tunnel` (see `internal/tunnel/AGENTS.md`). VPNs have no reload path at all; a VPN config change needs a full restart.

## Work Guidance

(none beyond the above)

## Verification

`go test ./internal/config/...`
