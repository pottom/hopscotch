# internal/vpn

## Purpose

Manages an `openconnect` subprocess per configured VPN: connect detection (stderr watching plus optional `ping_host` polling), backoff reconnect, pause/resume, graceful teardown (`post_disconnect`, DNS/route cleanup).

## Ownership

`vpn.go` (`Connection`: `Run`/pause-resume/state machine), `manager.go` (`Manager`: owns all `*Connection` by name; `WaitConnected`/`IsConnected` are consumed by `internal/tunnel` for VPN gating), `openconnect.go` (subprocess/arg construction), `process_unix.go`/`process_windows.go` (process-group kill).

## Local Contracts

- Same pre-`Run()` pause-then-restore safety pattern as `internal/tunnel` (`Run()` checks `paused` first, drains a stale `pauseRequest`) — see `internal/tunnel/AGENTS.md`; keep both in sync if this pattern ever changes.
- No config hot-reload exists for VPNs (unlike tunnels' `ApplyConfig`) — a VPN config change requires a full process restart.
- `connectedAt` is stored as `time.Now().Round(0)` (strips the monotonic clock reading), not raw `time.Now()` — otherwise the displayed "connected since" duration freezes during macOS sleep (monotonic clock doesn't advance while suspended). Same pattern as `internal/tunnel`'s `ConnectedAt`; apply it to any new timestamp captured here for duration display.

## Work Guidance

Debugging reconnect/timeout behavior: use `scripts/hs-watch.sh` for automated, timed observation instead of watching logs manually — DNS resolve, `ping_host` polling, and backoff timing are easy to misjudge by eye.

## Verification

`go test ./internal/vpn/...`; `scripts/hs-watch.sh` for live reconnect behavior.
