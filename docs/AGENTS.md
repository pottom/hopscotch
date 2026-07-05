# docs

## Purpose

Design/reference docs and visual assets: the canonical UI-parity spec (`DESIGN.md`), the full lifecycle flowchart (`lifecycle.md`), the REST API reference (`API.md`), architecture/flow SVGs, and TUI/web-UI screenshots used in the README and the served in-app readme.

## Ownership

`DESIGN.md`, `lifecycle.md`, `API.md`, all SVG/PNG assets.

## Local Contracts

- TUI/web-UI screenshots (`tui-*.png`, `ui-*.png`) are real captures from a fully synthetic fixture — regenerate all of them with `scripts/demo/screenshots.sh all` (see `scripts/AGENTS.md`), never by hand-editing or hand-drawing. It builds a throwaway local sshd + VPN stub + dummy HTTP target under `*.hs.test` (no real infrastructure or data), drives real traffic through it, and captures via `vhs` (TUI) and Playwright (web UI). Architecture/flow diagrams (`arch-overview.svg`, `flow-connection.svg`, `shared-proxy.svg`, `shell-demo.svg`, `logo.svg`) are the exception and stay hand-crafted SVGs. Whenever a screenshot changes, update it in **both** this directory and `internal/admin/ui/docs` (the served copy, `scripts/demo/screenshots.sh` does this automatically) — they must stay identical.
- `DESIGN.md` is the single canonical source for TUI/web UI parity — an undocumented difference between the two UIs is a bug (see `internal/tui/AGENTS.md`, `internal/admin/ui/AGENTS.md`).
- `lifecycle.md`'s mermaid diagram currently predates pause/resume, the PTY-poke (`pty_poke_interval`), and persisted paused-state (`internal/state.PausedTracker`) — needs an update pass; not currently accurate for those flows (see `internal/tunnel/AGENTS.md`).
- `API.md` documents every route registered in `internal/admin/server.go`'s `ListenAndServe` — when a route is added, removed, or its request/response shape changes, update `API.md` in the same change (see `internal/admin/AGENTS.md`).

## Work Guidance

(none beyond the above)

## Verification

(none — visual/diagram docs, verified by eye)
