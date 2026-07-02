# DOX framework

- DOX is highly performant AGENTS.md hierarchy installed here
- Agent must follow DOX instructions across any edits

## Core Contract

- AGENTS.md files are binding work contracts for their subtrees
- Work products, source materials, instructions, records, assets, and durable docs must stay understandable from the nearest applicable AGENTS.md plus every parent AGENTS.md above it

## Read Before Editing

1. Read the root AGENTS.md
2. Identify every file or folder you expect to touch
3. Walk from the repository root to each target path
4. Read every AGENTS.md found along each route
5. If a parent AGENTS.md lists a child AGENTS.md whose scope contains the path, read that child and continue from there
6. Use the nearest AGENTS.md as the local contract and parent docs for repo-wide rules
7. If docs conflict, the closer doc controls local work details, but no child doc may weaken DOX

Do not rely on memory. Re-read the applicable DOX chain in the current session before editing.

## Update After Editing

Every meaningful change requires a DOX pass before the task is done.

Update the closest owning AGENTS.md when a change affects:

- purpose, scope, ownership, or responsibilities
- durable structure, contracts, workflows, or operating rules
- required inputs, outputs, permissions, constraints, side effects, or artifacts
- user preferences about behavior, communication, process, organization, or quality
- AGENTS.md creation, deletion, move, rename, or index contents

Update parent docs when parent-level structure, ownership, workflow, or child index changes. Update child docs when parent changes alter local rules. Remove stale or contradictory text immediately. Small edits that do not change behavior or contracts may leave docs unchanged, but the DOX pass still must happen.

## Hierarchy

- Root AGENTS.md is the DOX rail: project-wide instructions, global preferences, durable workflow rules, and the top-level Child DOX Index
- Child AGENTS.md files own domain-specific instructions and their own Child DOX Index
- Each parent explains what its direct children cover and what stays owned by the parent
- The closer a doc is to the work, the more specific and practical it must be

## Child Doc Shape

- Create a child AGENTS.md when a folder becomes a durable boundary with its own purpose, rules, responsibilities, workflow, materials, or quality standards
- Work Guidance must reflect the current standards of the project or user instructions; if there are no specific standards or instructions yet, leave it empty
- Verification must reflect an existing check; if no verification framework exists yet, leave it empty and update it when one exists

Default section order:
- Purpose
- Ownership
- Local Contracts
- Work Guidance
- Verification
- Child DOX Index

## Style

- Keep docs concise, current, and operational
- Document stable contracts, not diary entries
- Put broad rules in parent docs and concrete details in child docs
- Prefer direct bullets with explicit names
- Do not duplicate rules across many files unless each scope needs a local version
- Delete stale notes instead of explaining history
- Trim obvious statements, repeated rules, misplaced detail, and warnings for risks that no longer exist

## Closeout

1. Re-check changed paths against the DOX chain
2. Update nearest owning docs and any affected parents or children
3. Refresh every affected Child DOX Index
4. Remove stale or contradictory text
5. Run existing verification when relevant
6. Report any docs intentionally left unchanged and why

## Project

hopscotch is an SSH tunnel manager with a built-in SOCKS5 proxy router: pattern-based rules send each connection through the right jump-host tunnel automatically, with a TUI and a web UI for status/control. Go 1.22+, `cobra` CLI (`cmd/`), `bubbletea` TUI (`internal/tui`), stdlib-only web admin server (`internal/admin`).

## Global Workflow

- Build: `./build.sh binary` (or `install`); embeds version via ldflags from `internal/version` — keep `LDFLAGS` in `build.sh` pointing at the current module path if `go.mod`'s module line ever changes, or the binary reports `version=dev`.
- Test: `go test ./...`; also run `go vet ./...` before considering a change done.
- `.claude/commands/debug.md` (`/debug`) — build, restart the daemon, live-monitor logs/status together with the user. Never commits, never kills processes outside the hopscotch daemon, never auto-fixes without showing the analysis first.
- `.claude/commands/release.md` (`/release`) — full release flow (changelog, doc/version bump, tests, tag). Stops for user approval at version-number and test-writing decisions.
- Commit convention: Conventional Commits prefixes (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`); commit body explains *why*, not what (the diff already shows what).
- `main` is protected — no direct pushes; every change lands via PR + CI.

## User Preferences

- **TUI/web UI parity is a hard rule** — same colors, text, ordering, state logic on both surfaces unless `docs/DESIGN.md` documents an accepted technical-constraint exception. A UI divergence not listed there is a bug. See `internal/tui/AGENTS.md`, `internal/admin/ui/AGENTS.md`.
- **Screenshots/mocks**: hand-crafted SVGs built with real instance data — never generate via browser automation/screenshotting. Update both `docs/` and `internal/admin/ui/docs/` together. See `docs/AGENTS.md`.
- **VPN/tunnel reconnect debugging**: use `scripts/hs-watch.sh` for automated, timed observation instead of eyeballing live logs — reconnect/backoff/ping timing is easy to misjudge manually. See `internal/vpn/AGENTS.md`.

## Child DOX Index

- [cmd/AGENTS.md](cmd/AGENTS.md) — CLI commands, process lifecycle, subsystem wiring
- [internal/admin/AGENTS.md](internal/admin/AGENTS.md) — HTTP admin server (API, auth, SSE streams)
  - [internal/admin/ui/AGENTS.md](internal/admin/ui/AGENTS.md) — embedded web UI assets
- [internal/config/AGENTS.md](internal/config/AGENTS.md) — config load/validate/write/reload
- [internal/notify/AGENTS.md](internal/notify/AGENTS.md) — native OS desktop notifications on meaningful tunnel/VPN state transitions
- [internal/proxy/AGENTS.md](internal/proxy/AGENTS.md) — SOCKS5 routing
- [internal/state/AGENTS.md](internal/state/AGENTS.md) — PID file and persisted app state
- [internal/tui/AGENTS.md](internal/tui/AGENTS.md) — terminal UI
- [internal/tunnel/AGENTS.md](internal/tunnel/AGENTS.md) — SSH tunnel lifecycle
- [internal/vpn/AGENTS.md](internal/vpn/AGENTS.md) — openconnect VPN subprocess lifecycle
- [pkg/socks5/AGENTS.md](pkg/socks5/AGENTS.md) — public SOCKS5 protocol implementation
- [deploy/AGENTS.md](deploy/AGENTS.md) — container packaging
- [docs/AGENTS.md](docs/AGENTS.md) — design spec, lifecycle diagram, screenshots
- [scripts/AGENTS.md](scripts/AGENTS.md) — operational scripts

Small single-purpose packages without package-specific rules beyond their godoc comments (`internal/keychain`, `internal/logger`, `internal/msgs`, `internal/netcheck`, `internal/security`, `internal/updater`, `internal/version`) are intentionally not indexed — read their source directly.
