# internal/tui

## Purpose

The `bubbletea` terminal UI: Status/Routes/Logs tabs, mirroring the web UI.

## Ownership

`model.go` — the entire TUI (rendering, key handling, viewport scrolling, log filters, rule editing).

## Local Contracts

- `docs/DESIGN.md` is the canonical spec for anything visual/behavioral shared with the web UI (colors, text, ordering, layout). A change here without a matching web UI change — or a documented exception in `DESIGN.md` — is a bug, not a valid divergence. See `internal/admin/ui/AGENTS.md`.
- `statusCursor` indexing convention: VPN rows occupy `[0, nVPNs)`, tunnel rows occupy `[nVPNs, nVPNs+nTunnels)`. This convention is repeated independently across rendering, key handling (`p`/`r`), and `lineOffsetForCursor` — keep all of them in sync if row ordering ever changes.
- The pause/resume/reconnect key handlers (`p`/`r`) call the admin HTTP API — the same endpoints the web UI uses — rather than any in-process manager directly. Do not add a direct in-process path; persistence/side-effects hooked at the HTTP layer (`internal/state.PausedTracker`, via `internal/admin`) would silently stop covering the TUI otherwise.
- Footer hints: `↑↓/jk` is shown as "scroll" only on tabs/states where it isn't already shown as "cursor" for the same keys — don't reintroduce the duplicate hint that was previously removed.

## Work Guidance

(none beyond `docs/DESIGN.md`)

## Verification

`go test ./internal/tui/...`; manual: run the TUI and diff against `docs/DESIGN.md`.
