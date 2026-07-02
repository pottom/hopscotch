# internal/admin/ui

## Purpose

The web UI itself: vanilla JS + Alpine.js + Chart.js + Pico CSS, embedded via `go:embed` in `../ui.go` and served entirely offline (no CDN calls at runtime).

## Ownership

`app.js`, `style.css`, `index.html`, `vendor/` (pinned copies of `alpine.min.js`, `ansi_up.js`, `chart.umd.min.js`, `marked.min.js`, `pico.min.css`), `docs/` (screenshot/diagram assets referenced by the served README).

## Local Contracts

- Every visual/behavioral element here must match the TUI per `docs/DESIGN.md` (the canonical, repo-root spec). A mismatch not documented there as an accepted exception is a bug, not a valid divergence.
- `vendor/` files are committed, pinned copies, not CDN references — hopscotch must render the web UI with zero internet access. Don't switch any of these to a `<script src="https://...">` CDN link.
- `docs/` here is a served **copy** of the repo-root `docs/` screenshots, not an independent set — when a screenshot changes, update both locations together (see root `AGENTS.md` and `docs/AGENTS.md`).

## Work Guidance

(none beyond `docs/DESIGN.md`)

## Verification

(no automated visual-parity check exists yet — verify by eye against `docs/DESIGN.md` and the TUI)
