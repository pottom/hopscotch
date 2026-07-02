# docs

## Purpose

Design/reference docs and visual assets: the canonical UI-parity spec (`DESIGN.md`), the full lifecycle flowchart (`lifecycle.md`), architecture/flow SVGs, and TUI/web-UI screenshots used in the README and the served in-app readme.

## Ownership

`DESIGN.md`, `lifecycle.md`, all SVG/PNG assets.

## Local Contracts

- Screenshots/mocks are hand-crafted SVGs built with real instance data — never generated via browser automation/screenshot tools. Whenever one changes, update it in **both** this directory and `internal/admin/ui/docs` (the served copy) — they must stay identical.
- `DESIGN.md` is the single canonical source for TUI/web UI parity — an undocumented difference between the two UIs is a bug (see `internal/tui/AGENTS.md`, `internal/admin/ui/AGENTS.md`).
- `lifecycle.md`'s mermaid diagram currently predates pause/resume, the PTY-poke (`pty_poke_interval`), and persisted paused-state (`internal/state.PausedTracker`) — needs an update pass; not currently accurate for those flows (see `internal/tunnel/AGENTS.md`).

## Work Guidance

(none beyond the above)

## Verification

(none — visual/diagram docs, verified by eye)
