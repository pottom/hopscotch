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

## Work Guidance

(none beyond the above)

## Verification

`go test ./internal/state/...`
