# internal/admin

## Purpose

HTTP admin server: session-cookie auth, `/health`, `/metrics` (Prometheus), `/status` (JSON), SSE streams (`/logs/stream`, `/traffic/stream`), the routing-rules editor API, tunnel/VPN reconnect-pause-resume actions, and serving the embedded web UI (`internal/admin/ui`).

## Ownership

`server.go` (`Server` struct, route table, auth middleware), `login.go`, `health.go`, `metrics.go`, `status.go`, `sse.go`, `logstream.go`, `rules.go`, `reconnect.go`, `readme.go`, `ui.go` (embed).

## Local Contracts

- `Server`'s fields and the small interfaces above them (`TunnelStatter`, `VPNStatter`, `RouteStatter`, `RuleUpdater`, `TunnelReconnecter`, `VPNReconnecter`) are the only contract with `internal/tunnel`/`internal/vpn`/`internal/proxy` — add a capability by extending/adding an interface here, not by reaching into the concrete manager types.
- `reconnect.go`'s four pause/resume handlers are the **only** code path that ever calls `tunnel.Manager`/`vpn.Manager` `Pause`/`Resume` — both the TUI and the web UI drive pause/resume exclusively through these HTTP endpoints. Anything hooked here (e.g. `internal/state.PausedTracker` persistence) automatically covers both UIs; don't add a second, direct-call path.
- `rules.go`'s `handleRules` mutates `cfg.Proxy.Rules` under `cfgMu` and immediately persists via `config.WriteConfig` (atomic temp-file + rename) before applying to the live router — `config.yaml` is the durable source of truth for rules, not just a bootstrap file. Hand-edited comments in `config.yaml` do not survive this write (see `internal/config/AGENTS.md`).
- Auth is all-or-nothing: `adminUsername == ""` means no auth for the whole server. `sessionToken` is random per process start, never persisted — restarting the daemon invalidates all sessions.

## Work Guidance

Any new capability exposed to both UIs must keep `docs/DESIGN.md` (canonical TUI/web UI parity spec) in sync — see `internal/tui/AGENTS.md` and `internal/admin/ui/AGENTS.md`.

## Verification

`go test ./internal/admin/...`

## Child DOX Index

- [ui/AGENTS.md](ui/AGENTS.md) — embedded web UI assets
