# internal/admin

## Purpose

HTTP admin server: session-cookie auth, `/health`, `/metrics` (Prometheus), `/status` (JSON), SSE streams (`/logs/stream`, `/traffic/stream`), the routing-rules editor API, the notification-settings editor API, tunnel/VPN reconnect-pause-resume actions, and serving the embedded web UI (`internal/admin/ui`).

## Ownership

`server.go` (`Server` struct, route table, auth middleware), `login.go`, `health.go`, `metrics.go`, `status.go`, `sse.go`, `logstream.go`, `rules.go`, `reconnect.go`, `notifications.go`, `readme.go`, `ui.go` (embed).

## Local Contracts

- `Server`'s fields and the small interfaces above them (`TunnelStatter`, `VPNStatter`, `RouteStatter`, `RuleUpdater`, `TunnelReconnecter`, `VPNReconnecter`, `NotifyController`) are the only contract with `internal/tunnel`/`internal/vpn`/`internal/proxy`/`internal/notify` — add a capability by extending/adding an interface here, not by reaching into the concrete manager types.
- `notifications.go`'s `handlePutNotifications` follows the exact same shape as `rules.go`'s `handleRules`: decode → `s.persistConfig(func(c *config.Config) { ... })` (the shared cfgMu-lock/mutate/copy/`config.WriteConfig` skeleton, in `server.go`) → apply live (`s.notifyCtl.SetConfig`, instead of a router/manager call). `/status`'s `Notifications` field (`notificationsJSON(s.notifyCtl.Config())`) is how both UIs read the current value to render the Settings tab — keep both in sync if `config.NotificationsConfig` grows a field. Any new live-editable config section should go through `persistConfig` too rather than re-copying its lock/copy/write block.
- `cmd/start.go`'s `config.WatchSIGHUP` reload callback must call `notifier.SetConfig(next.Notifications)` (alongside `mgr.ApplyConfig`/`router.UpdateRules`) — otherwise a hand-edited `notifications:` section in `config.yaml` only takes effect after a full restart, and `/status` (which reads `s.notifyCtl.Config()`, not `s.cfg`) keeps reporting the stale value even after other sections reload correctly. Any future live-editable config section needs the same SIGHUP wiring, not just a PUT handler.
- `reconnect.go`'s four pause/resume handlers are the code path that ever calls `tunnel.Manager`/`vpn.Manager` `Pause`/`Resume` **on behalf of a user** — both the TUI and the web UI drive pause/resume exclusively through these HTTP endpoints, so persistence (`internal/state.PausedTracker`) is hooked only here and automatically covers both UIs. One deliberate exception: `internal/tunnel`/`internal/vpn` each pause themselves directly (their own unexported `pause()`, not the exported `Pause()` the handlers use) when a configured `auto_pause_threshold` is reached (see those packages' `AGENTS.md`) — that path bypasses this file entirely and is intentionally **not** persisted (auto-pause is a computed reaction meant to re-evaluate on restart, not a stored user intent). `Stats().AutoPaused` (surfaced as `auto_paused` in `/status`) is how the two are told apart when `status`/`state == "paused"`; don't add any other second, direct-call path beyond that one.
- The same four `reconnect.go` handlers (reconnect + resume, tunnel and VPN) also call `s.suppressNotify(kind, name)` before triggering the action — a manual reconnect or resume drives `Status`/`State` through the same `Connecting` transient a real failure would, and without this, `internal/notify`'s watcher can't tell the two apart and fires a false "disconnected"/"reconnected" pair (found via live testing, not by inspection — see `internal/notify/AGENTS.md`). Manual `Pause` does **not** need suppression: pausing is bucketed as neither "down" nor "connected" by the watcher, so it never fires on its own. Any new manual action that forces a tunnel/VPN through a disconnect/reconnect cycle must call `suppressNotify` too.
- `rules.go`'s `handleRules` mutates `cfg.Proxy.Rules` under `cfgMu` and immediately persists via `config.WriteConfig` (atomic temp-file + rename) before applying to the live router — `config.yaml` is the durable source of truth for rules, not just a bootstrap file. Hand-edited comments in `config.yaml` do not survive this write (see `internal/config/AGENTS.md`).
- Auth is all-or-nothing: `adminUsername == ""` means no auth for the whole server. `sessionToken` is random per process start, never persisted — restarting the daemon invalidates all sessions.
- `startedAt` is stored as `time.Now().Round(0)` (strips the monotonic clock reading), not raw `time.Now()` — otherwise the daemon uptime shown in `/health`/`/status` (and thus the TUI/web UI header) freezes during macOS sleep, since the monotonic clock doesn't advance while the system is suspended. Same pattern applied to `ConnectedAt`/`connectedAt` in `internal/tunnel`/`internal/vpn` — any new "since this moment" timestamp meant for duration display must follow it too.

## Work Guidance

Any new capability exposed to both UIs must keep `docs/DESIGN.md` (canonical TUI/web UI parity spec) in sync — see `internal/tui/AGENTS.md` and `internal/admin/ui/AGENTS.md`. Any route added, removed, or changed in `ListenAndServe`'s route table must keep `docs/API.md` in sync too — it documents every endpoint's request/response shape for external scripting, not just the two bundled UIs.

## Verification

`go test ./internal/admin/...`

## Child DOX Index

- [ui/AGENTS.md](ui/AGENTS.md) — embedded web UI assets
