# cmd

## Purpose

`cobra`-based CLI: all hopscotch subcommands (`start`, `stop`, `status`, `logs`, `ping`, `vpn`, `trust`, `update`, `validate`, `ssh-config`, `shell-init`, `enable`, `disable`, `proxy-connect`). `root.go` wires global flags (`--config`, `--verbose`, `--log-file`) and gates logger init in `PersistentPreRunE`.

## Ownership

Everything under `cmd/`. `start.go` additionally owns process lifecycle (daemonize, PID file via `internal/state`, signal handling) and is the single place that constructs and wires together `tunnel.Manager`, `vpn.Manager`, `proxy.Router`, `proxy.Server`, and `admin.Server`.

## Local Contracts

- `start.go` is the only wiring point for top-level subsystems — a new subsystem (new manager, new server) gets constructed and started (`g.Go(...)`, `errgroup`) there, not scattered across other commands.
- `checkKeys` (SSH key permission check) must run before daemonizing — it's interactive/visible only in the foreground parent process.
- The "already running" PID check runs unconditionally, even with `--foreground` (skipped only in container mode via `HOPSCOTCH_CONTAINER=true`, which also redirects `internal/state`'s file paths to `/tmp`). `--restart` sends SIGTERM, waits 5s, then SIGKILL, before proceeding.
- Startup order in `runStart`: load config → resolve `internal/state.NewPausedTracker` (config dir) → construct managers → pre-apply persisted pause state to each manager *before* any `Run()` goroutine starts → construct `admin.Server` (passing the same tracker) → start all `Run()` goroutines via `errgroup`.

## Work Guidance

Use the `/debug` skill (`.claude/commands/debug.md`) for the standard build → restart → log-monitor → analyze loop; use `/release` (`.claude/commands/release.md`) for cutting a release. Build via `./build.sh` (embeds version/commit/date via ldflags).

## Verification

`go build ./...`; manual smoke test via `/debug`.
