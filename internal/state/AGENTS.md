# internal/state

## Purpose

Two independent, differently-scoped on-disk files that happen to share a package and a filename:

1. **PID file + a `state.json` in the OS cache dir** (`state.go`) — ephemeral runtime info, deleted by `Manager.Remove()` on clean shutdown/restart.
2. **A `state.json` next to `config.yaml`** (`paused.go`) — durable user setting (which tunnels/VPNs are paused), never deleted, restored on every start.

## Ownership

`state.go` — `Manager`: `WritePID`/`ReadPID`/`Remove`, plus an unused/dead `Write`/`Read` pair for the cache-dir `state.json` snapshot (kept, not wired to anything — do not assume it's live).
`paused.go` — `PausedTracker`/`PersistedState`/`PausedState`: the actually-used, actually-restored `state.json`.

## Local Contracts

- The two "`state.json`" files are **not the same file** — same name, different directories (`os.UserCacheDir()/hopscotch` vs. `filepath.Dir(cfg.Path)`), different lifecycles. Don't merge their logic without checking both call sites (`cmd/start.go` constructs both, separately).
- `PausedTracker`'s load/save **never** return errors to the caller — any read/parse/write failure is `log.Warn`'d internally and treated as "nothing paused" / best-effort save. This is deliberate: a broken state file must never block startup or break pause/resume at runtime. Preserve this contract in any extension.
- All `PausedTracker` methods are nil-receiver-safe (a nil tracker behaves as empty/no-op) — relied on by tests that construct `admin.Server` without one.
- `PersistedState` nests fields under a named key (`"paused": {...}`) specifically so future state can be added as sibling keys without a schema/file rename — follow this pattern for new persisted fields rather than adding new top-level files.
- **Where a new field belongs — `config.yaml` vs. `state.json` vs. nowhere** — decide by asking what the value *is*, not just whether it needs to survive a restart:
  - **Declared policy** ("what should happen"): tunnels/VPNs/rules, thresholds, credentials, notification preferences. Portable — a user might version-control, back up, or copy `config.yaml` to another machine and expect the same behavior there. Goes in `config.yaml`, even though every app-driven write there fully regenerates the file (see `internal/config/AGENTS.md`) — that's an accepted cost once a UI edits it.
  - **Operator-driven runtime intent** ("what I told this instance to do, right now"): the paused set is the only current example. Not portable — you would *not* want a tunnel to start paused on a different machine just because it's paused here. Still needs to survive a restart on *this* machine, so it goes in `state.json`, not `config.yaml`.
  - **Purely computed status** ("what's currently true, derived from policy + live probing"): auto-pause (`Stats.AutoPaused`), connection status, traffic counters. Never persisted anywhere — always recomputed from scratch at startup (see `internal/admin/AGENTS.md`'s auto-pause note). Don't add a field here just because a value happens to change at runtime; only operator intent that must outlive a restart belongs in this file.
  - When in doubt: "would I want this copied if I `scp`'d `config.yaml` to a second machine?" — yes → `config.yaml`; no but it must survive *this* machine's restart → `state.json`; no because it's derived, not decided → don't persist it.

## Work Guidance

(none beyond the above)

## Verification

`go test ./internal/state/...`
