# cmd

## Purpose

`cobra`-based CLI: all hopscotch subcommands (`start`, `stop`, `status`, `logs`, `ping`, `vpn`, `tunnel`, `trust`, `update`, `validate`, `ssh-config`, `shell-init`, `enable`, `disable`, `proxy-connect`). `root.go` wires global flags (`--config`, `--verbose`, `--log-file`) and gates logger init in `PersistentPreRunE`.

## Ownership

Everything under `cmd/`. `start.go` additionally owns process lifecycle (daemonize, PID file via `internal/state`, signal handling) and is the single place that constructs and wires together `tunnel.Manager`, `vpn.Manager`, `proxy.Router`, `proxy.Server`, and `admin.Server`.

## Local Contracts

- `start.go` is the only wiring point for top-level subsystems — a new subsystem (new manager, new server) gets constructed and started (`g.Go(...)`, `errgroup`) there, not scattered across other commands.
- `checkKeys` (SSH key permission check) must run before daemonizing — it's interactive/visible only in the foreground parent process.
- The "already running" PID check runs unconditionally, even with `--foreground` (skipped only in container mode via `HOPSCOTCH_CONTAINER=true`, which also redirects `internal/state`'s file paths to `/tmp`). `--restart` sends SIGTERM, waits 5s, then SIGKILL, before proceeding.
- Startup order in `runStart`: load config → resolve `internal/state.NewPausedTracker` (config dir) → construct managers → pre-apply persisted pause state to each manager *before* any `Run()` goroutine starts → construct `admin.Server` (passing the same tracker) → start all `Run()` goroutines via `errgroup`.
- `config.WatchSIGHUP`'s reload callback in `runStart` must apply *every* live-reloadable config section, not just the ones a given change happens to touch — it currently calls `mgr.ApplyConfig`, `router.UpdateRules`, `refreshSSHConfig`, and `notifier.SetConfig`. Missing one here means a hand-edited `config.yaml` section silently keeps its stale in-memory value after a SIGHUP reload until a full restart (found for `notifier.SetConfig` via code review, not live testing — see `internal/admin/AGENTS.md`). A new live-editable section needs a matching call added here too.
- `tunnel.go`'s `hopscotch tunnel add` is a CLI-side config editor (append a `TunnelConfig`, `config.Validate` the whole in-memory `*Config` before `config.WriteConfig` — never write first and validate after) — it does not touch a running daemon at all; the new tunnel only takes effect after a SIGHUP reload or restart, same as any other hand-edit. `config.ApplyDefaults`/`config.Validate` are exported specifically so a CLI wizard can normalize/check an in-memory config before persisting it — reuse them (don't re-open `Load`'s file-parsing path or hand-roll validation) for any future `<subcommand> add`-style command (e.g. a hypothetical `hopscotch vpn add`). Critically, `ApplyDefaults` is only ever run on a disposable, deep-copied scratch struct used purely to validate — **never** on the struct that gets passed to `WriteConfig`: `ApplyDefaults` expands a leading `~/` to this machine's literal absolute home directory and fills in every numeric default in place, and `Load()` already reapplies the same defaults on every read, so running it on the write-path candidate would silently replace the user's portable `~/...` and bake in a wall of default values that don't appear in any hand-written tunnel. The scratch copy needs its own deep copy of *every* slice field that carries structs (`Tunnels` and `VPNs`, not just the one the command is appending to) — a bare `*cfg` struct-copy only copies slice headers, so mutating the scratch copy's entries in place would alias and corrupt the real config's backing array.

## Work Guidance

Use the `/debug` skill (`.claude/commands/debug.md`) for the standard build → restart → log-monitor → analyze loop; use `/release` (`.claude/commands/release.md`) for cutting a release. Build via `./build.sh` (embeds version/commit/date via ldflags).

## Verification

`go build ./...`; manual smoke test via `/debug`.
