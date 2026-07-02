# UI Design Spec

## Core principle: TUI ↔ Web UI parity

The TUI and the web UI **must look and behave identically wherever feasible.** This means:

- Identical colors (down to the hex value)
- Identical text, prefixes, icons
- Identical ordering and layout
- Identical status indicators and logic

If a change is made on one surface — whether a color, a text, or a new element — **it must automatically be carried over to the other surface as well.** No separate request is needed for that.

Exceptions (where a divergence is acceptable due to a technical constraint):
- Interaction elements (keyboard handling in the TUI, hover/click in the web UI)
- Animations (limited in the TUI)
- Separator style (TUI = space, web UI = `·`)
- Wrapping and alignment (terminal character grid vs. CSS flexbox)

This document is the canonical reference. If something isn't documented here but a divergence exists between the two surfaces, that must be treated as a bug.

---

## Settings tab — native desktop notification toggles

The `notifications` config is live-editable on both surfaces, on a fourth
"Settings" tab (`Status` / `Rules` / `Logs` / `Settings`). Five checkboxes:
`Enabled`, `Notify on disconnect`, `Notify on reconnect`, `Notify on
auto-pause`, `Play sound`. The checkboxes are subordinate to `Enabled` — when
it's off, the other four are inactive/dimmed (`var(--muted)` / TUI
`styleMuted`), with identical logic on both surfaces.

Saves immediately on toggle (no separate "Edit"/"Save" button, unlike the
Rules tab) — `PUT /api/notifications`, which persists to `config.yaml` and
applies live to the running daemon (no restart needed). TUI: `↑↓`/`jk` moves
the cursor, `space`/`enter` toggles. Web UI: native checkbox with `x-model`,
saved on every `@change`. See `internal/notify/AGENTS.md`, `internal/admin/AGENTS.md`.

---

## Header

**Canonical order (left → right):**

```
hopscotch vX.Y.Z  [⚡new_version]  [badge]  [● iface localIP / ○ no link]  [⊕ internet publicIP / ○ no internet]  PID XXXXX  up Xh Xm
```

| Element | Always visible | Condition |
|------|---------------|----------|
| `hopscotch vX.Y.Z` | yes | — |
| `⚡X.Y.Z` (update badge) | no | when a newer version is available |
| status badge (`healthy` / `degraded` / ...) | yes | — |
| `● iface localIP` / `○ no link` | yes | based on uplink state; localIP = the interface's local IP; red if there's no link |
| `⊕ internet publicIP` | no | when `admin.show_public_ip: true` and internet is available |
| `○ no internet` | no | when there's a link but no internet (`admin.show_public_ip: true`) |
| `PID XXXXX` | yes | — |
| `up Xh Xm` | yes | — |

**Separator:** TUI = two spaces, web UI = `·` character.

---

## Status table — Name column colors

| Row type | Color | Hex |
|------------|------|-----|
| VPN | teal | `#2dd4bf` |
| Tunnel | sky blue | `#38bdf8` |
| direct | violet | `#a78bfa` |

---

## Status table — Host / Iface column

Both surfaces: `var(--muted)` / `colorMuted (#475569)`.

---

## Status table — VPN column (in tunnel rows)

Format: `● vpnname` if connected, `○ vpnname` if not.

Color: based on the VPN's current state (`colorConnected` / `colorConnecting` / `colorDisconnected`).

---

## Status table — Cursor and reconnect

The status table has a cursor (`statusCursor`) that shows which tunnel is selected for the `r` (reconnect) action.

| Element | Appearance |
|------|-----------|
| Selected row prefix | `> ` amber / `colorConnecting (#fbbf24)` |
| Unselected prefix | `  ` (two spaces) |

The viewport automatically scrolls to the cursor position (`lineOffsetForCursor()`). The cursor doesn't wrap: `k`/`↑` does nothing on the first tunnel, and neither does `j`/`↓` on the last.

In the web UI: a ↻ button, hover-reveal (per row, on the right edge). Color: `var(--muted)` by default, `var(--accent)` on hover.

---

## Status table — ↓ / ↑ columns (traffic)

In the table, the ↓/↑ columns show a **cumulative total** (since process start):

| Value | Display |
|-------|-------------|
| 0 | `—` |
| < 1 KB | `X B` |
| < 1 MB | `X.X KB` |
| < 1 GB | `X.X MB` |
| ≥ 1 GB | `X.X GB` |

Column header: `↓ TOTAL` / `↑ TOTAL`.

The per-second rate (bps) is shown exclusively in the graph area — the graph's first row (in non-compact mode) is the `↓ X B/s  ↑ X B/s` line above the braille graph. In the web UI, the `#bps-bar-{name}` div in the expanded graph row. Color: `colorBpsIn (#38bdf8)` / `colorBpsOut (#818cf8)`.

---

## Status table — Error/progress sub-row

Appears under every tunnel and VPN row when `last_error` is non-empty and the state isn't `connected`.

| Type | Prefix | Color |
|-------|--------|------|
| Progress (waiting for...) | `◌ ` | amber / `var(--connecting)` |
| Error | `└ ✗ ` | red / `var(--disconnected)` |

Root-cause propagation: if a tunnel has `last_error = "waiting for VPN: X"` and VPN X has its own `last_error`, that one must be displayed (not the "waiting for VPN: X" text).

---

## Status table — Reconnect timer text

```
○ next try: Xs
```

Identical text on both surfaces. TUI: `renderStatus()`, web UI: `tunnelStatusHtml()` / `vpnStatusHtml()`.

---

## Status table — Paused text (manual vs. auto-pause)

```
⏸ paused           ← manual pause (from TUI/web UI)
⏸ paused (auto)    ← triggered by auto_pause_threshold
```

The `(auto)` indicator only appears when the app itself triggered the pause
(due to reaching `auto_pause_threshold`), not the user. In addition, as the
threshold is approached (in the connecting/disconnected state, before the
pause), both surfaces also show the `⚠N/threshold` count indicator.

Identical text/logic on both surfaces. TUI: `renderStatus()`, web UI:
`tunnelStatusHtml()` / `vpnStatusHtml()`.

---

## Footer (TUI)

```
[hints]                                           PROXY bind:port  ADMIN bind:port
```

The hints line above, the ports line below, right-aligned.

---

## Logs tab — filter row

The Logs tab header has two rows above the viewport:

**Row 1 — severity + source badges:**
```
  INFO+   TUNNEL  ·  VPN  ·  PROXY  ·  SYS
```

| Element | Active | Inactive |
|------|-------|---------|
| Severity (ALL / INFO+ / WARN+ / ERR) | `colorAccent (#38bdf8)` | — |
| Source badge (TUNNEL / VPN / PROXY / SYS) | `colorVPN (#2dd4bf)`, bold | `colorMuted (#475569)` |

**Row 2 — text filter input:**
```
  / Filter… — Ctrl+N to clear
```

When focused, the `/` prefix is `colorAccent`; when unfocused, `colorMuted`.

**Keys (Logs tab):**

| Key | Effect |
|-----------|-------|
| `l` | Severity cycle: ALL → INFO+ → WARN+ → ERR |
| `t` / `v` / `p` / `s` | Source toggle: tunnel / vpn / proxy / system |
| `/` | Activate text filter |
| `Esc` | Leave text filter |
| `Ctrl+N` | Clear text filter |

**AND logic:** all three filters (`level`, `source`, `grep`) apply simultaneously. At least one source always stays active.

**Web UI equivalents:** severity chips (blue), source chips (teal), Filter… input — identical visual logic.

---

## Color palette

| Variable | Hex | Role |
|---------|-----|--------|
| `--connected` / `colorConnected` | `#34d399` | connected state |
| `--connecting` / `colorConnecting` | `#fbbf24` | connecting / progress |
| `--disconnected` / `colorDisconnected` | `#f87171` | error / disconnected |
| `--accent` / `colorAccent` | `#38bdf8` | tunnel names, active tab |
| `colorVPN` | `#2dd4bf` | VPN names |
| `colorDirect` | `#a78bfa` | direct row/via |
| `--muted` / `colorMuted` | `#475569` | secondary text |
| `colorBpsIn` | `#38bdf8` | inbound traffic |
| `colorBpsOut` | `#818cf8` | outbound traffic |
