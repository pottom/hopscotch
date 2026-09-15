# internal/vpn

## Purpose

Manages an `openconnect` subprocess per configured VPN: connect detection (stderr watching plus optional `ping_host` polling), backoff reconnect, pause/resume, graceful teardown (`post_disconnect`, DNS/route cleanup).

## Ownership

`vpn.go` (`Connection`: `Run`/pause-resume/state machine), `manager.go` (`Manager`: owns all `*Connection` by name; `IsConnected`/`IsPaused` are consumed by `internal/tunnel` for VPN gating), `openconnect.go` (subprocess/arg construction), `process_unix.go`/`process_windows.go` (process-group kill).

## Local Contracts

- Same pre-`Run()` pause-then-restore safety pattern as `internal/tunnel` (`Run()` checks `paused` first, drains a stale `pauseRequest`) — see `internal/tunnel/AGENTS.md`; keep both in sync if this pattern ever changes.
- Several VPNs can be configured at once (e.g. two concentrators into the same network, switched by pausing one and resuming the other), so every signal/kill targets **this** VPN's process only: `terminateProcs`/`killOrphanedProcs` use `pkill -f` with `procPattern` (binary as argv[0], server URL as the last argument — `buildArgs` must keep the server last). Never go back to a name-only `pkill -x openconnect`; it tears down the other VPN, which then restarts and kills this one in turn.
- After a switch every VPN reports the same interface name (Linux reuses `tun0` once the old device is gone), so the name alone never identifies a connection's device. `setTunIface` stores the kernel index next to the name, `flushTunRoutes` flushes only while that index still matches (otherwise it would wipe the newly started VPN's routes), and `runPostDisconnect` clears both so a paused VPN doesn't keep displaying a device it no longer has.
- openconnect prints errors to stderr but progress (`Connected as`, `Set up tun device`) to **stdout**; both are routed into one `os.Pipe` read by `watchOutput`. Keep both pipe ends `*os.File` (not `cmd.StdoutPipe`/an `io.Writer`) — otherwise exec adds a copy goroutine that `cmd.Wait()` blocks on while an orphaned child holds the write end.
- When `ping_host` is set it is the only thing that marks a VPN `connected`; `watchOutput` promotes on `Connected as`/`Established …` only without `ping_host`. After switching between VPNs the session is up long before internal hosts answer (measured 15–35 s), and promoting early makes tunnels dial into an unrouted network and burn their auto-pause budget.
- `connect_timeout` (default 15 s) bounds how long `ping_host` may stay unreachable after launch. If the tunnel interface did appear, `pollPingHost` sets `retryNow` and `Run()` restarts without the backoff delay, up to `maxQuickRetries` (2) times in a row (`quickRetries`, reset on connect/resume), then backs off normally. Measured live on two VPNs into one network (2026-09-15): some sessions come up but never pass traffic (tun0 tx rising, rx 0 — the gateway returns nothing; one stayed dark for a full 60 s timeout) while the next session often answers within ~2 s — so don't "fix" slow switches by lengthening the timeout or dropping the quick retry. Don't raise the cap either: after ~25 sessions in ten minutes the gateway stayed dark for 16 minutes.
- The first successful `ping_host` probe marks the VPN connected (a TCP handshake already proves two-way traffic); don't reintroduce a multi-probe confirmation. Measure changes here with `scripts/hs-watch.sh` or a timed pause/resume switch, not by eye.
- No config hot-reload exists for VPNs (unlike tunnels' `ApplyConfig`) — a VPN config change requires a full process restart.
- `connectedAt` is stored as `time.Now().Round(0)` (strips the monotonic clock reading), not raw `time.Now()` — otherwise the displayed "connected since" duration freezes during macOS sleep (monotonic clock doesn't advance while suspended). Same pattern as `internal/tunnel`'s `ConnectedAt`; apply it to any new timestamp captured here for duration display.
- `consecutiveFailures` (`atomic.Int32`) counts connection attempts in a row that never reached `StateConnected` — reset at the same `connectedAt.Load().(time.Time).After(beforeRun)` check that already resets the backoff, and on `Resume()`. When `cfg.AutoPauseThreshold > 0` and the count reaches it, `Run()` triggers the same pause mechanics as the public `Pause()` (mirrors `internal/tunnel`'s identical mechanism — see that package's `AGENTS.md` for the shared rationale: not persisted via `internal/state.PausedTracker`, a deliberate second exception to the admin-handlers-only Pause/Resume contract in `internal/admin/AGENTS.md`, and `Resume()` always resets the counter regardless of pause reason).
- `Pause()`/`pauseLocked()` split, the `pauseMu` mutex serializing pause/resume/auto-pause/auto-resume transitions, `autoPaused` flag, and `Stats().AutoPaused` all mirror `internal/tunnel` exactly (down to the field and method names) — see that package's `AGENTS.md` for the precise rule about which of `Pause()`/`pauseLocked()` to call from where, and for why `pauseMu` exists (a TOCTOU race between a concurrent `Pause()`/`Resume()` and the auto-resume timer's check-then-mutate sequence). Keep both packages' pause/auto-pause code in sync if this mechanism changes in either.
- `cfg.AutoResumeAfter` (seconds, 0 disables) mirrors `internal/tunnel`'s auto-resume cooldown exactly, down to the manual-pause-during-cooldown race guard (re-check `autoPaused` under `pauseMu` when the timer fires, don't call `Resume()` itself to avoid leaving a stale token in the buffered `resume` channel) — see that package's `AGENTS.md` for the full rationale. One difference: this package has no `Clock` abstraction, so the cooldown uses a plain `time.After` rather than `t.clock.After`.

## Work Guidance

Debugging reconnect/timeout behavior: use `scripts/hs-watch.sh` for automated, timed observation instead of watching logs manually — DNS resolve, `ping_host` polling, and backoff timing are easy to misjudge by eye.

## Verification

`go test ./internal/vpn/...`; `scripts/hs-watch.sh` for live reconnect behavior.
