# internal/tui

## Purpose

The `bubbletea` terminal UI: Status/Routes/Logs/Settings tabs, mirroring the web UI.

## Ownership

`model.go` — the entire TUI (rendering, key handling, viewport scrolling, log filters, rule editing).

## Local Contracts

- `docs/DESIGN.md` is the canonical spec for anything visual/behavioral shared with the web UI (colors, text, ordering, layout). A change here without a matching web UI change — or a documented exception in `DESIGN.md` — is a bug, not a valid divergence. See `internal/admin/ui/AGENTS.md`.
- `statusCursor` indexing convention: VPN rows occupy `[0, nVPNs)`, tunnel rows occupy `[nVPNs, nVPNs+nTunnels)`. This convention is repeated independently across rendering, key handling (`p`/`r`), and `lineOffsetForCursor` — keep all of them in sync if row ordering ever changes.
- The pause/resume/reconnect key handlers (`p`/`r`) call the admin HTTP API — the same endpoints the web UI uses — rather than any in-process manager directly. Do not add a direct in-process path; persistence/side-effects hooked at the HTTP layer (`internal/state.PausedTracker`, via `internal/admin`) would silently stop covering the TUI otherwise.
- Footer hints: `↑↓/jk` is shown as "scroll" only on tabs/states where it isn't already shown as "cursor" for the same keys — don't reintroduce the duplicate hint that was previously removed.
- Settings tab: `settingsCursor` indexes into the package-level `settingsRows` slice (label + get/set closures over `admin.NotificationsJSON`) — a separate, independent convention from `statusCursor`'s VPN/tunnel indexing, since it's a small fixed list, not a dynamic one. Toggling calls `toggleSettingsRow`, a *value*-receiver method returning `(Model, tea.Cmd)`; don't change it to a pointer receiver used as `return m, m.toggleSettingsRow()` — Go evaluates the bare `m` before the pointer-mutating call runs, so the toggle would silently not appear in the returned model.
- `settingsSaving`/`settingsResendNeeded` implement a depth-1 request queue, not a plain in-flight bool: a toggle that lands while a save is already in flight sets `settingsResendNeeded` instead of firing a second concurrent PUT, and `settingsSavedMsg`/`settingsSaveErrMsg` check it and immediately re-fire `saveSettingsCmd` before clearing `settingsSaving`. This guarantees at most one `/api/notifications` PUT in flight at a time from this client — without it, two rapid toggles' responses can arrive out of order and either clobber a newer save with a stale one, or clear `settingsSaving` early and let an intervening `/status` poll roll back the optimistic toggle for ~1s. The web UI's `notificationsSaving`/`notificationsResendNeeded` in `app.js` mirror this exactly — keep both in sync (see `internal/admin/ui/AGENTS.md`).
- `settingsSavedAt` drives a transient "✓ saved" line in `buildSettingsContent` after a successful save, cleared by a `tea.Tick(1500ms)` (`settingsSavedTimeoutCmd`) — mirrors the web UI's `#settings-save-status` "Saved ✓" + 1500ms `setTimeout`. Required by the TUI/web UI parity rule above; don't let the TUI regress back to "saving…"/error-only with no success state.
- `case "enter", " "` in the main key switch must check `m.activeTab == tabSettings` *first* and only handle the key there; letting it fall through unconditionally breaks bubbles' default `PageDown` binding (space) on Status/Rules/Logs, which rely on the key reaching the active viewport's own `Update()` in the `default:` dispatch. Any new tab-specific key binding added to this switch needs the same tab-scoping, not a global case.
- `saveRulesCmd`/`saveSettingsCmd` both build their own URL/body then delegate the marshal→PUT→truncated-error-body→result-Msg mechanics to the shared `putJSONCmd(client, url, body, onOK, onErr)` — add any new Settings/Rules-shaped save through that helper rather than re-copying the `http.NewRequest`/status-check block.

## Work Guidance

(none beyond `docs/DESIGN.md`)

## Verification

`go test ./internal/tui/...`; manual: run the TUI and diff against `docs/DESIGN.md`.
