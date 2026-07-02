# internal/tunnel

## Purpose

Manages SSH tunnel lifecycle: dial, keepalive, backoff reconnect, optional VPN gating, optional forced PTY session (for SPS/SCB jump-host channel policy), pause/resume.

## Ownership

`tunnel.go` (`Tunnel`: `Run`/dial/keepalive/PTY/pause-resume), `manager.go` (`Manager`: owns all `*Tunnel` by name, `ApplyConfig` hot-reload, the admin-facing interfaces), `status.go` (`Stats`/`Status`), `agent_watcher.go` (SSH-agent key-change watcher for instant retry after e.g. inserting a YubiKey).

## Local Contracts

- `Run()`'s loop checks `paused.Load()` as the very first thing every iteration, and drains a stale buffered `pauseRequest` signal right after — this makes calling `Pause()` *before* `Run()` ever starts (used to restore persisted pause state at boot, see `internal/state/AGENTS.md` and `cmd/AGENTS.md`) safe and race-free. Preserve this ordering in any refactor.
- `force_pty` opens a background PTY shell session purely to satisfy SCB/SPS channel policy — nothing is ever executed in it. `pty_poke_interval` (deliberately independent of, and much coarser than, `keepalive_interval`) writes a harmless space+backspace into that session's stdin on its own timer so a session-recording SCB doesn't treat the channel as idle. Keep this decoupled from `keepalive_interval` — if it fired as often as a typical keepalive (seconds), the recorded session would balloon.
- The SSH-level keepalive (`keepalive@openssh.com` global request, transport-scoped) and the PTY-poke (channel-scoped data) solve two different failure modes — don't conflate or merge them.
- `ApplyConfig` (SIGHUP reload) only adds/removes tunnels whose *name* changed in config; an existing same-named tunnel keeps its original `*Tunnel` pointer and in-memory state (including `paused`) untouched. A tunnel that's removed and later re-added under the same name via a live reload gets a fresh, unpaused `*Tunnel` even if the persisted state file still lists it as paused — known gap, not solved (see `internal/state/AGENTS.md`).
- `docs/lifecycle.md`'s mermaid diagram documents this package's control flow in detail but currently predates pause/resume and `pty_poke_interval` — treat it as historical for those two behaviors, not authoritative, until it's updated (see `docs/AGENTS.md`).
- `ConnectedAt` is stored as `now.Round(0)` (strips the monotonic clock reading), not the raw `time.Now()`/`t.clock.Now()` — otherwise `time.Since(ConnectedAt)` (used for the displayed "connected since" duration in `internal/admin`) freezes during macOS sleep, since the monotonic clock doesn't advance while the system is suspended. Any new timestamp captured here for later duration display must do the same; the shared `Clock`/`realClock.Now()` abstraction itself is left untouched (it also drives reconnect/backoff timing and is mocked by tests) — strip monotonic only at the specific field-assignment site.

## Work Guidance

(none beyond the above)

## Verification

`go test ./internal/tunnel/...`
