# scripts

## Purpose

Operational scripts not part of the built binary.

## Ownership

`hs-watch.sh` — automated, timed observation of VPN/tunnel reconnect behavior.
`demo/` — synthetic fixture + capture pipeline that regenerates the TUI/web-UI documentation screenshots (`docs/tui-*.png`, `docs/ui-*.png`).

## Local Contracts

- Use this script (not manual log-watching) when debugging VPN/tunnel reconnect timing — see `internal/vpn/AGENTS.md`.
- `demo/screenshots.sh {setup|warm|capture|teardown|all}` is the only supported way to regenerate `docs/tui-*.png`/`docs/ui-*.png` — see `docs/AGENTS.md`. It builds a fully synthetic fixture (throwaway local sshd, a VPN stub binary, a dummy HTTP target, fake hostnames under `*.hs.test` aliased to `127.0.0.1` via a temporary, clearly-marked `/etc/hosts` block) so hopscotch really connects, really pauses, and really moves bytes — no real infrastructure or data, no hand-drawn mocks. Captures via `vhs` (real terminal) and Playwright (real browser). `teardown` (which `all` always runs, even on failure, via a top-level `trap`) removes the `/etc/hosts` block and kills every spawned process. `setup` is self-healing: it checks for a leftover `$STATE_FILE` from a previous interrupted run *before* creating anything new and runs `teardown` on it first — a run interrupted before `setup` finishes (e.g. `Ctrl-C` mid-build) can still leak processes started before the state file was written, so if `setup` reports something unexpected, check for orphaned `sshd`/`python3`/`fake-openconnect.sh` processes and a leftover `# hopscotch-demo BEGIN/END` block in `/etc/hosts` manually.
- `fake-openconnect.sh`'s `TERM`/`INT` trap must explicitly `kill` the backgrounded `sleep` it's currently waiting on before exiting — a bare `exit 0` doesn't touch a running background job, so the child would otherwise be orphaned (reparented, sleeping for up to an hour) instead of dying with its parent. Track the child's PID in a variable set right after backgrounding it, and reference that variable (not a fresh `$!`) in the trap.
- `demo/capture_web.js` wraps every Playwright call after `chromium.launch()` in a `try { ... } finally { await browser.close(); }` — without it, a `page.waitForSelector`/`waitForFunction` timeout on any single screenshot step throws past `browser.close()` and leaks a chromium process on every failed run, which compounds badly for a script meant to be re-run repeatedly.
- Requires `vhs`+`ttyd` (`brew install vhs`) and Playwright with its Chromium browser already available on the machine — both are one-time setup, not re-checked by the script.

## Work Guidance

(none beyond the above)

## Verification

`hs-watch.sh`: (none). `demo/screenshots.sh`: run `all`, then eyeball the 8 output PNGs (no `0 B/s`, no real hostnames/IPs, every intended feature state visible) before considering a regeneration done.
